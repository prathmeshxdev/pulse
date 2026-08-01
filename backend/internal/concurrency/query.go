package concurrency

import (
	"fmt"
	"strings"
	"time"

	"github.com/prathmeshxdev/pulse/internal/filters"
	"github.com/prathmeshxdev/pulse/internal/querybuilder"
)

// Grain is the reporting bucket. Minute curve is always the primitive.
type Grain string

const (
	GrainMinute Grain = "minute"
	GrainHour   Grain = "hour"
	GrainDay    Grain = "day"
)

// Metric selects the response shape.
type Metric string

const (
	MetricPeak       Metric = "peak"
	MetricAvg        Metric = "avg"
	MetricTimeseries Metric = "timeseries"
	MetricSummary    Metric = "summary" // peak + avg
)

// Request is the POST /api/v1/concurrency/chart body.
type Request struct {
	Start   time.Time       `json:"start"`
	End     time.Time       `json:"end"`
	Grain   Grain           `json:"grain"`
	Metric  Metric          `json:"metric"`
	Filters []filters.Filter `json:"filters"`
}

// Query holds the compiled SQL and a stable cache key.
type Query struct {
	SQL      string
	CacheKey string
}

// BuildChartQuery compiles the normative benchmark template (SCHEMA_AND_DDL).
// Never reads raw_events. Omits the sel CTE when there are no dimension filters
// (FINAL_PLAN §15.4 unselective-case optimisation).
func BuildChartQuery(req Request, database string, maxSegmentSpanHours int) (Query, error) {
	if req.End.Before(req.Start) || req.End.Equal(req.Start) {
		return Query{}, fmt.Errorf("end must be after start")
	}
	if req.Grain == "" {
		req.Grain = GrainMinute
	}
	if req.Metric == "" {
		req.Metric = MetricSummary
	}
	if maxSegmentSpanHours <= 0 {
		maxSegmentSpanHours = 72
	}

	preds, hasFilters, err := filters.BuildSegmentPredicates(req.Filters, database)
	if err != nil {
		return Query{}, err
	}

	startLit := formatDT(req.Start)
	endLit := formatDT(req.End)
	lookback := fmt.Sprintf("INTERVAL %d HOUR", maxSegmentSpanHours)

	// Inline the range as non-null DateTime literals. A `params` CTE referenced
	// via scalar subqueries makes range_start/range_end Nullable(DateTime), and
	// numbers(dateDiff(Nullable,...)) is rejected ("Illegal type Nullable(Int64),
	// must be numeric type"). Inlining keeps every use non-nullable.
	rangeStart := fmt.Sprintf("toDateTime(%s, 'UTC')", startLit)
	rangeEnd := fmt.Sprintf("toDateTime(%s, 'UTC')", endLit)

	b := querybuilder.New("")

	var segIN string
	if hasFilters {
		selWhere := []string{
			fmt.Sprintf("segment_start < %s", rangeEnd),
			fmt.Sprintf("segment_end > %s", rangeStart),
			fmt.Sprintf("segment_start >= %s - %s", rangeStart, lookback),
		}
		selWhere = append(selWhere, preds...)
		selSQL := fmt.Sprintf(
			"SELECT segment_id\nFROM %s.session_active_segments FINAL\nWHERE %s",
			database, strings.Join(selWhere, "\n  AND "),
		)
		b.WithRaw("sel", selSQL)
		segIN = "AND segment_id IN (SELECT segment_id FROM sel)"
	}

	openingSQL := fmt.Sprintf(`SELECT sum(delta) AS c0
FROM %s.minute_deltas
WHERE minute >= %s - %s
  AND minute < %s
  %s`, database, rangeStart, lookback, rangeStart, segIN)
	b.WithRaw("opening", openingSQL)

	netSQL := fmt.Sprintf(`SELECT minute, sum(delta) AS net
FROM %s.minute_deltas
WHERE minute >= %s
  AND minute < %s
  %s
GROUP BY minute`, database, rangeStart, rangeEnd, segIN)
	b.WithRaw("net", netSQL)

	b.WithRaw("grid", fmt.Sprintf(`SELECT %s + toIntervalMinute(number) AS minute
FROM numbers(dateDiff('minute', %s, %s))`, rangeStart, rangeStart, rangeEnd))

	b.WithRaw("curve", `SELECT
    g.minute AS minute,
    ifNull((SELECT c0 FROM opening), 0)
        + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
FROM grid AS g
LEFT JOIN net AS n ON g.minute = n.minute`)

	applyMetric(b, req)
	return Query{
		SQL:      b.Build(),
		CacheKey: cacheKey(req, database, maxSegmentSpanHours),
	}, nil
}

// applyMetric adds the final SELECT over the `curve` CTE for the requested
// metric/grain. Shared by the narrow and rollup query builders.
func applyMetric(b *querybuilder.Builder, req Request) {
	switch req.Metric {
	case MetricTimeseries:
		switch req.Grain {
		case GrainHour:
			b.From("curve").
				Select("minute", "toStartOfHour(minute) AS bucket").
				Select("peak", "max(concurrency) AS peak").
				Select("avg", "avg(concurrency) AS avg").
				GroupBy("bucket", "bucket").
				OrderBy("bucket", "bucket")
		case GrainDay:
			b.From("curve").
				Select("minute", "toStartOfDay(minute) AS bucket").
				Select("peak", "max(concurrency) AS peak").
				Select("avg", "avg(concurrency) AS avg").
				GroupBy("bucket", "bucket").
				OrderBy("bucket", "bucket")
		default:
			b.From("curve").
				Select("minute", "minute").
				Select("concurrency", "concurrency").
				OrderBy("minute", "minute")
		}
	case MetricPeak:
		b.From("curve").Select("peak", "max(concurrency) AS peak_concurrency")
	case MetricAvg:
		b.From("curve").Select("avg", "avg(concurrency) AS avg_concurrency")
	default: // summary
		b.From("curve").
			Select("peak", "max(concurrency) AS peak_concurrency").
			Select("avg", "avg(concurrency) AS avg_concurrency")
	}
}

// BuildRollupQuery compiles the same curve against the wide rollup
// (concurrency_minute_serving): dimensions are predicates ON the delta rows, so
// there is no segment semi-join. Same opening balance + dense grid + cumsum, so
// answers are identical to the narrow path (verified). Callers use this only when
// filters.RollupSupported is true.
func BuildRollupQuery(req Request, database string, maxSegmentSpanHours int) (Query, error) {
	if !req.End.After(req.Start) {
		return Query{}, fmt.Errorf("end must be after start")
	}
	if req.Grain == "" {
		req.Grain = GrainMinute
	}
	if req.Metric == "" {
		req.Metric = MetricSummary
	}
	if maxSegmentSpanHours <= 0 {
		maxSegmentSpanHours = 72
	}
	preds, err := filters.BuildRollupPredicates(req.Filters)
	if err != nil {
		return Query{}, err
	}
	dimWhere := ""
	if len(preds) > 0 {
		dimWhere = "AND " + strings.Join(preds, "\n  AND ")
	}
	rangeStart := fmt.Sprintf("toDateTime(%s, 'UTC')", formatDT(req.Start))
	rangeEnd := fmt.Sprintf("toDateTime(%s, 'UTC')", formatDT(req.End))
	lookback := fmt.Sprintf("INTERVAL %d HOUR", maxSegmentSpanHours)
	tbl := database + ".concurrency_minute_serving"

	b := querybuilder.New("")
	b.WithRaw("opening", fmt.Sprintf(`SELECT sum(delta) AS c0
FROM %s
WHERE minute >= %s - %s AND minute < %s
  %s`, tbl, rangeStart, lookback, rangeStart, dimWhere))
	b.WithRaw("net", fmt.Sprintf(`SELECT minute, sum(delta) AS net
FROM %s
WHERE minute >= %s AND minute < %s
  %s
GROUP BY minute`, tbl, rangeStart, rangeEnd, dimWhere))
	b.WithRaw("grid", fmt.Sprintf(`SELECT %s + toIntervalMinute(number) AS minute
FROM numbers(dateDiff('minute', %s, %s))`, rangeStart, rangeStart, rangeEnd))
	b.WithRaw("curve", `SELECT
    g.minute AS minute,
    ifNull((SELECT c0 FROM opening), 0)
        + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
FROM grid AS g
LEFT JOIN net AS n ON g.minute = n.minute`)

	applyMetric(b, req)
	return Query{SQL: b.Build(), CacheKey: "rollup|" + cacheKey(req, database, maxSegmentSpanHours)}, nil
}

func formatDT(t time.Time) string {
	return "'" + t.UTC().Format("2006-01-02 15:04:05") + "'"
}

func cacheKey(req Request, database string, span int) string {
	var b strings.Builder
	b.WriteString(database)
	b.WriteByte('|')
	b.WriteString(req.Start.UTC().Format(time.RFC3339))
	b.WriteByte('|')
	b.WriteString(req.End.UTC().Format(time.RFC3339))
	b.WriteByte('|')
	b.WriteString(string(req.Grain))
	b.WriteByte('|')
	b.WriteString(string(req.Metric))
	b.WriteByte('|')
	b.WriteString(fmt.Sprintf("%d", span))
	for _, f := range req.Filters {
		b.WriteByte('|')
		b.WriteString(f.Dimension)
		b.WriteByte('=')
		b.WriteString(f.Op)
		b.WriteByte(':')
		b.WriteString(f.Value)
		if len(f.Values) > 0 {
			b.WriteString(strings.Join(f.Values, ","))
		}
	}
	return b.String()
}
