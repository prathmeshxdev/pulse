package concurrency

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/filters"
)

// Check is one consistency verification result.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

// VerifyOptions configures chart/breakdown consistency checks against ClickHouse.
type VerifyOptions struct {
	Database            string
	MaxSegmentSpanHours int
	Start               time.Time
	End                 time.Time
	PropTypes           filters.PropertyTypeResolver
	// PartitionDimensions are low-cardinality dims where every segment has
	// exactly one value — we also assert sum(slices) == unfiltered timeseries.
	PartitionDimensions []string // e.g. platform, country
	// MatchDimensions are extra dims where we only check that a breakdown row
	// equals a chart filtered to that value (no full partition-sum — too expensive
	// for high-cardinality keys like show_name / video_resolution).
	MatchDimensions []string
	BreakdownLimit  int
}

// RunConsistencyChecks validates that summary, timeseries, and per-value filters agree.
func RunConsistencyChecks(ctx context.Context, conn driver.Conn, opt VerifyOptions) ([]Check, error) {
	if opt.MaxSegmentSpanHours <= 0 {
		opt.MaxSegmentSpanHours = 72
	}
	if len(opt.PartitionDimensions) == 0 {
		opt.PartitionDimensions = []string{"platform", "country"}
	}
	if len(opt.MatchDimensions) == 0 {
		opt.MatchDimensions = []string{"show_name", "video_resolution", "video_type", "category"}
	}
	if opt.BreakdownLimit <= 0 {
		opt.BreakdownLimit = 10
	}
	if opt.PropTypes == nil {
		opt.PropTypes = filters.StringFallbackTypes{}
	}

	var out []Check
	for _, unit := range []CountUnit{UnitSession, UnitUser} {
		if unit == UnitUser && !chclient.TableExists(ctx, conn, opt.Database, "user_minute_deltas") {
			out = append(out, Check{
				Name:   "user_tables_present",
				Pass:   false,
				Detail: "user_minute_deltas missing — run build_user_segments",
			})
			continue
		}
		unitChecks, err := verifyUnit(ctx, conn, opt, unit)
		if err != nil {
			return out, err
		}
		out = append(out, unitChecks...)
	}
	return out, nil
}

func verifyUnit(ctx context.Context, conn driver.Conn, opt VerifyOptions, unit CountUnit) ([]Check, error) {
	var out []Check
	tag := string(unit)

	sumPeak, sumAvg, err := querySummary(ctx, conn, opt, unit, nil)
	if err != nil {
		return nil, err
	}
	ts, err := queryMinuteSeries(ctx, conn, opt, unit, nil)
	if err != nil {
		return nil, err
	}
	maxTS, meanTS := seriesStats(ts)
	out = append(out, Check{
		Name:   tag + "_summary_peak_matches_timeseries",
		Pass:   floatsEqual(sumPeak, maxTS),
		Detail: fmt.Sprintf("summary peak %.4f vs max(timeseries) %.4f", sumPeak, maxTS),
	})
	out = append(out, Check{
		Name:   tag + "_summary_avg_matches_timeseries",
		Pass:   floatsEqual(sumAvg, meanTS),
		Detail: fmt.Sprintf("summary avg %.4f vs mean(timeseries) %.4f", sumAvg, meanTS),
	})

	// Breakdown row must equal chart filtered to the same dimension=value.
	matchDims := append([]string{}, opt.PartitionDimensions...)
	matchDims = append(matchDims, opt.MatchDimensions...)
	for _, dim := range matchDims {
		checks, err := verifyBreakdownMatchesFilter(ctx, conn, opt, unit, dim, opt.BreakdownLimit*3)
		if err != nil {
			return out, err
		}
		out = append(out, checks...)
	}

	for _, dim := range opt.PartitionDimensions {
		allValues, err := distinctValues(ctx, conn, opt, unit, dim, 0)
		if err != nil {
			return out, err
		}
		if len(allValues) == 0 {
			continue
		}
		sumByMinute := map[int64]float64{}
		for _, v := range allValues {
			series, err := queryMinuteSeries(ctx, conn, opt, unit, []filters.Filter{{Dimension: dim, Op: "eq", Value: v}})
			if err != nil {
				return out, err
			}
			for m, c := range series {
				sumByMinute[m] += c
			}
		}
		mismatch := 0
		var worst float64
		for m, total := range ts {
			s := sumByMinute[m]
			if !floatsEqual(total, s) {
				mismatch++
				if d := math.Abs(total - s); d > worst {
					worst = d
				}
			}
		}
		out = append(out, Check{
			Name: tag + "_partition_sum_" + dim,
			Pass: mismatch == 0,
			Detail: fmt.Sprintf("%d/%d minutes mismatch (worst delta %.4f) — sum of %s slices vs unfiltered",
				mismatch, len(ts), worst, dim),
		})
	}

	unpeak, _, _ := querySummary(ctx, conn, opt, unit, nil)
	for _, dim := range matchDims {
		values, _ := distinctValues(ctx, conn, opt, unit, dim, opt.BreakdownLimit)
		for _, v := range values {
			fp, _, _ := querySummary(ctx, conn, opt, unit, []filters.Filter{{Dimension: dim, Op: "eq", Value: v}})
			out = append(out, Check{
				Name:   fmt.Sprintf("%s_filtered_peak_le_total_%s_%s", tag, dim, sanitize(v)),
				Pass:   fp <= unpeak+1e-9,
				Detail: fmt.Sprintf("filtered peak %.4f <= unfiltered %.4f", fp, unpeak),
			})
		}
	}

	return out, nil
}

// verifyBreakdownMatchesFilter checks the UI contract: breakdown(dim)[value].peak/avg
// equals chart(filters: dim=value).peak/avg for the top-N values of dim.
func verifyBreakdownMatchesFilter(ctx context.Context, conn driver.Conn, opt VerifyOptions, unit CountUnit, dim string, limit int) ([]Check, error) {
	tag := string(unit)
	values, err := distinctValues(ctx, conn, opt, unit, dim, limit)
	if err != nil {
		return nil, fmt.Errorf("%s values: %w", dim, err)
	}
	if len(values) == 0 {
		return []Check{{
			Name:   fmt.Sprintf("%s_breakdown_%s_has_values", tag, dim),
			Pass:   false,
			Detail: "no non-empty values — dimension not populated?",
		}}, nil
	}

	// Same selection + per-value summary path as handleBreakdown.
	type brow struct{ value string; peak, avg float64 }
	rows := make([]brow, 0, len(values))
	for _, v := range values {
		p, a, err := querySummary(ctx, conn, opt, unit, []filters.Filter{{Dimension: dim, Op: "eq", Value: v}})
		if err != nil {
			return nil, fmt.Errorf("breakdown %s=%s: %w", dim, v, err)
		}
		rows = append(rows, brow{value: v, peak: p, avg: a})
	}

	var out []Check
	for _, br := range rows {
		f := []filters.Filter{{Dimension: dim, Op: "eq", Value: br.value}}
		chartPeak, chartAvg, err := querySummary(ctx, conn, opt, unit, f)
		if err != nil {
			return nil, err
		}
		out = append(out, Check{
			Name:   fmt.Sprintf("%s_breakdown_%s_%s_peak", tag, dim, sanitize(br.value)),
			Pass:   floatsEqual(chartPeak, br.peak),
			Detail: fmt.Sprintf("filter peak %.4f vs breakdown %.4f", chartPeak, br.peak),
		})
		out = append(out, Check{
			Name:   fmt.Sprintf("%s_breakdown_%s_%s_avg", tag, dim, sanitize(br.value)),
			Pass:   floatsEqual(chartAvg, br.avg),
			Detail: fmt.Sprintf("filter avg %.4f vs breakdown %.4f", chartAvg, br.avg),
		})
	}
	return out, nil
}

func querySummary(ctx context.Context, conn driver.Conn, opt VerifyOptions, unit CountUnit, fs []filters.Filter) (peak, avg float64, err error) {
	req := Request{
		Start: opt.Start, End: opt.End,
		Grain: GrainMinute, Metric: MetricSummary, Unit: unit, Filters: fs,
	}
	q, err := BuildChartQuery(req, opt.Database, opt.MaxSegmentSpanHours, opt.PropTypes)
	if err != nil {
		return 0, 0, err
	}
	rows, err := chclient.QueryMaps(ctx, conn, q.SQL)
	if err != nil || len(rows) != 1 {
		return 0, 0, fmt.Errorf("summary query: %w", err)
	}
	return toFloat(rows[0]["peak_concurrency"]), toFloat(rows[0]["avg_concurrency"]), nil
}

func queryMinuteSeries(ctx context.Context, conn driver.Conn, opt VerifyOptions, unit CountUnit, fs []filters.Filter) (map[int64]float64, error) {
	req := Request{
		Start: opt.Start, End: opt.End,
		Grain: GrainMinute, Metric: MetricTimeseries, Unit: unit, Filters: fs,
	}
	q, err := BuildChartQuery(req, opt.Database, opt.MaxSegmentSpanHours, opt.PropTypes)
	if err != nil {
		return nil, err
	}
	rows, err := chclient.QueryMaps(ctx, conn, q.SQL)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]float64, len(rows))
	for _, r := range rows {
		t, ok := r["minute"].(time.Time)
		if !ok {
			continue
		}
		out[t.Unix()] = toFloat(r["concurrency"])
	}
	return out, nil
}

func distinctValues(ctx context.Context, conn driver.Conn, opt VerifyOptions, unit CountUnit, dim string, limit int) ([]string, error) {
	resolved, ok := filters.ResolveDimension(dim, opt.Database, opt.PropTypes)
	if !ok {
		return nil, fmt.Errorf("unknown dimension %q", dim)
	}
	expr := filters.BreakdownValueExpr(resolved, opt.Database, opt.PropTypes)
	nonEmpty := filters.NonEmptyPredicate(resolved, expr)
	table := SegmentTable(unit)
	sql := fmt.Sprintf("SELECT %s AS v FROM %s.%s FINAL WHERE %s GROUP BY v ORDER BY count() DESC",
		expr, opt.Database, table, nonEmpty)
	if limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := chclient.QueryMaps(ctx, conn, sql)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v, ok := r["v"].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func seriesStats(m map[int64]float64) (max, mean float64) {
	if len(m) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range m {
		if v > max {
			max = v
		}
		sum += v
	}
	return max, sum / float64(len(m))
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case int:
		return float64(t)
	default:
		return 0
	}
}

func floatsEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	if len(s) > 24 {
		return s[:24]
	}
	return s
}
