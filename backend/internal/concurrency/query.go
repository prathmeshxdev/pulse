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

// CountUnit is the concurrency counting primitive (session-aware vs user-level).
type CountUnit string

const (
	UnitSession CountUnit = "session" // video_session_id — default
	UnitUser    CountUnit = "user"    // merged user_id islands — session-independent
)

// ParseCountUnit normalizes API/bench/config values.
func ParseCountUnit(s string) CountUnit {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "user", "user_id":
		return UnitUser
	default:
		return UnitSession
	}
}

// Request is the POST /api/v1/concurrency/chart body.
type Request struct {
	Start   time.Time        `json:"start"`
	End     time.Time        `json:"end"`
	Grain   Grain            `json:"grain"`
	Metric  Metric           `json:"metric"`
	Unit    CountUnit        `json:"unit,omitempty"`
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
//
// Folds in still-open sessions (open_edges CTE) so "active now" queries don't
// have to wait for a segment to close and flush to minute_deltas — see the
// open_edges doc comment below for the mechanism and why is_final=0 is the
// wrong filter for it.
func BuildChartQuery(req Request, database string, maxSegmentSpanHours int, propTypes filters.PropertyTypeResolver) (Query, error) {
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
	unit := req.Unit
	if unit == "" {
		unit = UnitSession
	}
	segTable, deltaTable, idCol := tablesFor(unit)

	preds, hasFilters, err := filters.BuildSegmentPredicates(req.Filters, database, propTypes)
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
			"SELECT %s\nFROM %s.%s FINAL\nWHERE %s",
			idCol, database, segTable, strings.Join(selWhere, "\n  AND "),
		)
		b.WithRaw("sel", selSQL)
		segIN = fmt.Sprintf("AND %s IN (SELECT %s FROM sel)", idCol, idCol)
	}

	openWhere := []string{
		fmt.Sprintf("segment_end > %s - %s", rangeStart, lookback),
		fmt.Sprintf("segment_start < %s", rangeEnd),
	}
	openWhere = append(openWhere, preds...)
	openWhereSQL := strings.Join(openWhere, "\n    AND ")

	if unit == UnitUser {
		b.WithRaw("open_edges", fmt.Sprintf(`SELECT toStartOfMinute(min(segment_start)) AS minute, 1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s
GROUP BY user_id
UNION ALL
SELECT toStartOfMinute(subtractMilliseconds(max(segment_end), 1)) + toIntervalMinute(1) AS minute, -1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s
GROUP BY user_id`, database, openWhereSQL))
	} else {
		b.WithRaw("open_edges", fmt.Sprintf(`SELECT toStartOfMinute(segment_start) AS minute, 1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s
UNION ALL
SELECT toStartOfMinute(subtractMilliseconds(segment_end, 1)) + toIntervalMinute(1) AS minute, -1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s`, database, openWhereSQL))
	}

	openingSQL := fmt.Sprintf(`SELECT sum(delta) AS c0 FROM (
    SELECT delta FROM %[1]s.%[2]s
    WHERE minute >= %[3]s - %[4]s AND minute < %[3]s
    %[5]s
    UNION ALL
    SELECT delta FROM open_edges
    WHERE minute >= %[3]s - %[4]s AND minute < %[3]s
)`, database, deltaTable, rangeStart, lookback, segIN)
	b.WithRaw("opening", openingSQL)

	netSQL := fmt.Sprintf(`SELECT minute, sum(delta) AS net FROM (
    SELECT minute, delta FROM %[1]s.%[2]s
    WHERE minute >= %[3]s AND minute < %[4]s
    %[5]s
    UNION ALL
    SELECT minute, delta FROM open_edges
    WHERE minute >= %[3]s AND minute < %[4]s
)
GROUP BY minute`, database, deltaTable, rangeStart, rangeEnd, segIN)
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
		CacheKey: cacheKey(req, database, maxSegmentSpanHours, unit),
	}, nil
}

func tablesFor(unit CountUnit) (segTable, deltaTable, idCol string) {
	if unit == UnitUser {
		return "user_active_segments", "user_minute_deltas", "user_segment_id"
	}
	return "session_active_segments", "minute_deltas", "segment_id"
}

// SegmentTable returns the serving segment table for the counting unit.
func SegmentTable(unit CountUnit) string {
	t, _, _ := tablesFor(unit)
	return t
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
	return Query{SQL: b.Build(), CacheKey: "rollup|" + cacheKey(req, database, maxSegmentSpanHours, UnitSession)}, nil
}

func formatDT(t time.Time) string {
	return "'" + t.UTC().Format("2006-01-02 15:04:05") + "'"
}

func cacheKey(req Request, database string, span int, unit CountUnit) string {
	var b strings.Builder
	b.WriteString(database)
	b.WriteByte('|')
	b.WriteString(string(unit))
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
