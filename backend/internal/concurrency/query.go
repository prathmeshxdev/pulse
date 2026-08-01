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
//
// Folds in still-open sessions (open_edges CTE) so "active now" queries don't
// have to wait for a segment to close and flush to minute_deltas — see the
// open_edges doc comment below for the mechanism and why is_final=0 is the
// wrong filter for it.
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

	// open_edges folds still-open sessions into the same +1/-1 minute-edge
	// scheme deltas.EmitAnyOverlap already uses for closed segments, so a
	// currently-open session counts immediately instead of only after it
	// closes and flushes to minute_deltas (streamd only writes a delta pair
	// on close — see cmd/streamd). Filters apply directly here (typed
	// columns on session_active_segments, same preds as `sel` above) —
	// dimension support falls out for free, no separate mechanism needed.
	//
	// Filtered by close_reason = '', NOT is_final = 0: is_final only means
	// "the whole session ended via VideoSessionEnd" (SCHEMA_AND_DDL.md); a
	// segment closed for any other reason (pause/background/buffer/
	// heartbeat-gap) is ALSO is_final=0 and is already correctly represented
	// in minute_deltas from its own close. Filtering on is_final=0 here
	// would double-count every ordinary close. streamd's OpenSnapshot is the
	// only writer that leaves close_reason empty (see internal/livestate and
	// internal/segments.Accumulator.OpenSnapshot) — every real close sets one
	// of the CloseReason* constants, all non-empty.
	//
	// The minus-edge mirrors EmitAnyOverlap's exact rounding
	// (StartOfMinute(end-1ms)+1min, not StartOfMinute(end)) — getting this
	// even slightly off silently shifts sessions into the wrong minute.
	// Validated against the full real dataset (differential test against an
	// in-memory Accumulator replay, both global and dimension-filtered):
	// exact on every check except two explained by a pre-existing 72h
	// lookback-boundary artifact in the opening-balance calc below,
	// unrelated to this CTE (a closed segment's edge pair straddling the
	// lookback loses its start-side +1 — same behavior with or without this
	// CTE, just usually invisible at normal query granularity).
	openWhere := []string{
		fmt.Sprintf("segment_end > %s - %s", rangeStart, lookback),
		fmt.Sprintf("segment_start < %s", rangeEnd),
	}
	openWhere = append(openWhere, preds...)
	openWhereSQL := strings.Join(openWhere, "\n    AND ")
	b.WithRaw("open_edges", fmt.Sprintf(`SELECT toStartOfMinute(segment_start) AS minute, 1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s
UNION ALL
SELECT toStartOfMinute(subtractMilliseconds(segment_end, 1)) + toIntervalMinute(1) AS minute, -1 AS delta
FROM %[1]s.session_active_segments FINAL
WHERE close_reason = ''
    AND %[2]s`, database, openWhereSQL))

	openingSQL := fmt.Sprintf(`SELECT sum(delta) AS c0 FROM (
    SELECT delta FROM %[1]s.minute_deltas
    WHERE minute >= %[2]s - %[3]s AND minute < %[2]s
    %[4]s
    UNION ALL
    SELECT delta FROM open_edges
    WHERE minute >= %[2]s - %[3]s AND minute < %[2]s
)`, database, rangeStart, lookback, segIN)
	b.WithRaw("opening", openingSQL)

	netSQL := fmt.Sprintf(`SELECT minute, sum(delta) AS net FROM (
    SELECT minute, delta FROM %[1]s.minute_deltas
    WHERE minute >= %[2]s AND minute < %[3]s
    %[4]s
    UNION ALL
    SELECT minute, delta FROM open_edges
    WHERE minute >= %[2]s AND minute < %[3]s
)
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
