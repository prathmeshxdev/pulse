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

	b := querybuilder.New("")
	b.WithRaw("params", fmt.Sprintf(
		"SELECT toDateTime(%s, 'UTC') AS range_start, toDateTime(%s, 'UTC') AS range_end",
		startLit, endLit,
	))

	var segIN string
	if hasFilters {
		selWhere := []string{
			"segment_start < (SELECT range_end FROM params)",
			"segment_end > (SELECT range_start FROM params)",
			fmt.Sprintf("segment_start >= (SELECT range_start FROM params) - %s", lookback),
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
WHERE minute >= (SELECT range_start FROM params) - %s
  AND minute < (SELECT range_start FROM params)
  %s`, database, lookback, segIN)
	b.WithRaw("opening", openingSQL)

	netSQL := fmt.Sprintf(`SELECT minute, sum(delta) AS net
FROM %s.minute_deltas
WHERE minute >= (SELECT range_start FROM params)
  AND minute < (SELECT range_end FROM params)
  %s
GROUP BY minute`, database, segIN)
	b.WithRaw("net", netSQL)

	b.WithRaw("grid", `SELECT (SELECT range_start FROM params) + toIntervalMinute(number) AS minute
FROM numbers(dateDiff('minute', (SELECT range_start FROM params), (SELECT range_end FROM params)))`)

	b.WithRaw("curve", `SELECT
    g.minute AS minute,
    ifNull((SELECT c0 FROM opening), 0)
        + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
FROM grid AS g
LEFT JOIN net AS n ON g.minute = n.minute`)

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

	sql := b.Build()
	return Query{
		SQL:      sql,
		CacheKey: cacheKey(req, database, maxSegmentSpanHours),
	}, nil
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
