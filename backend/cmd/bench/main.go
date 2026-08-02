// bench runs the benchmark query set against the serving layer, emitting
// answers.json, per-query latency, and system.query_log / system.parts evidence.
// It reuses the exact query compiler the API uses, so benchmark answers and
// dashboard answers come from identical SQL (FINAL_PLAN §10, VALIDATION Layer/runner).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/concurrency"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/filters"
)

// Case is one benchmark question. Start/End are optional; when empty the full
// data window (min/max minute in minute_deltas) is used — which is what makes
// the same spec valid for the unseen day with no edits.
type Case struct {
	Name    string           `json:"name"`
	Start   string           `json:"start,omitempty"`
	End     string           `json:"end,omitempty"`
	Grain   string           `json:"grain"`
	Metric  string           `json:"metric"`
	Unit    string           `json:"unit,omitempty"` // session (default) | user
	Filters []filters.Filter `json:"filters,omitempty"`
}

type answer struct {
	Case      Case    `json:"case"`
	QueryID   string  `json:"query_id"`
	Peak      *float64 `json:"peak,omitempty"`
	Avg       *float64 `json:"avg,omitempty"`
	RowCount  int     `json:"row_count"`
	LatencyMS float64 `json:"latency_ms"`
	SQL       string  `json:"sql,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
	specPath := flag.String("spec", "", "benchmark spec JSON (default: auto-generated grain summaries over full window)")
	outDir := flag.String("out", "evidence", "output directory for answers.json + evidence")
	configPath := flag.String("config", "", "path to config.env")
	includeSQL := flag.Bool("sql", true, "include compiled SQL in answers.json")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: bench -dsn clickhouse://user:pass@host:9440/sony_liv?secure=true")
		os.Exit(2)
	}
	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}

	ctx := context.Background()
	conn, err := chclient.Connect(ctx, *dsn)
	must(err, "connect")
	defer conn.Close()

	winStart, winEnd, err := dataWindow(ctx, conn, cfg.Database)
	must(err, "data window")
	fmt.Printf("data window: %s .. %s\n", winStart.Format(time.RFC3339), winEnd.Format(time.RFC3339))

	cases, err := loadCases(*specPath, conn, ctx, cfg.Database)
	must(err, "load cases")

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		must(err, "mkdir")
	}

	answers := make([]answer, 0, len(cases))
	queryIDs := make([]string, 0, len(cases))
	for i, c := range cases {
		start, end := winStart, winEnd
		if c.Start != "" {
			start, _ = time.Parse(time.RFC3339, c.Start)
		}
		if c.End != "" {
			end, _ = time.Parse(time.RFC3339, c.End)
		}
		req := concurrency.Request{
			Start: start.UTC(), End: end.UTC(),
			Grain: grainOr(c.Grain), Metric: metricOr(c.Metric), Filters: c.Filters,
			Unit: concurrency.ParseCountUnit(c.Unit),
		}
		q, err := concurrency.BuildChartQuery(req, cfg.Database, cfg.MaxSegmentSpanHours, nil)
		a := answer{Case: c}
		if *includeSQL {
			a.SQL = q.SQL
		}
		if err != nil {
			a.Error = err.Error()
			answers = append(answers, a)
			continue
		}
		qid := fmt.Sprintf("pulse-bench-%d", i)
		a.QueryID = qid
		queryIDs = append(queryIDs, qid)

		qctx := clickhouse.Context(ctx, clickhouse.WithQueryID(qid))
		t0 := time.Now()
		rows, err := chclient.QueryMaps(qctx, conn, q.SQL)
		a.LatencyMS = float64(time.Since(t0).Microseconds()) / 1000.0
		if err != nil {
			a.Error = err.Error()
			answers = append(answers, a)
			continue
		}
		a.RowCount = len(rows)
		if len(rows) == 1 {
			a.Peak = numPtr(rows[0]["peak_concurrency"])
			a.Avg = numPtr(rows[0]["avg_concurrency"])
		}
		fmt.Printf("  %-28s grain=%-6s peak=%s avg=%s rows=%d %.1fms\n",
			c.Name, req.Grain, fmtPtr(a.Peak), fmtPtr(a.Avg), a.RowCount, a.LatencyMS)
		answers = append(answers, a)
	}

	ql := queryLogEvidence(ctx, conn, queryIDs)
	writeJSON(filepath.Join(*outDir, "answers.json"), answers)
	writeJSON(filepath.Join(*outDir, "parts.json"), partsEvidence(ctx, conn, cfg.Database))
	writeJSON(filepath.Join(*outDir, "query_log.json"), ql)
	printPerf(ql)
	fmt.Printf("wrote %s/{answers,parts,query_log}.json\n", *outDir)
}

// printPerf summarizes server-side execution from system.query_log — the fair
// performance number (excludes client/network round-trip).
func printPerf(rows []map[string]any) {
	if len(rows) == 0 {
		return
	}
	durs := make([]float64, 0, len(rows))
	var maxRows float64
	for _, r := range rows {
		durs = append(durs, toF64(r["query_duration_ms"]))
		if rr := toF64(r["read_rows"]); rr > maxRows {
			maxRows = rr
		}
	}
	sort.Float64s(durs)
	q := func(p float64) float64 { return durs[int(math.Min(float64(len(durs)-1), p*float64(len(durs)))) ] }
	fmt.Printf("server-side latency: p50=%.0fms p90=%.0fms max=%.0fms | max rows read=%.0f (n=%d)\n",
		q(0.5), q(0.9), durs[len(durs)-1], maxRows, len(durs))
}

func toF64(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	}
	return 0
}

// dataWindow reads the served time range so a spec needs no hard-coded dates.
func dataWindow(ctx context.Context, conn driver.Conn, db string) (time.Time, time.Time, error) {
	rows, err := chclient.QueryMaps(ctx, conn,
		fmt.Sprintf("SELECT min(minute) AS a, max(minute) + toIntervalMinute(1) AS b FROM %s.minute_deltas", db))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if len(rows) == 0 || rows[0]["a"] == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("minute_deltas is empty — run the pipeline first")
	}
	a, _ := rows[0]["a"].(time.Time)
	b, _ := rows[0]["b"].(time.Time)
	return a.UTC(), b.UTC(), nil
}

// loadCases reads a spec file, or generates the default set: unfiltered
// peak/avg at minute, hour, day over the full window, plus a top-platform and
// top-country filtered summary discovered from the data.
func loadCases(path string, conn driver.Conn, ctx context.Context, db string) ([]Case, error) {
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var cs []Case
		if err := json.Unmarshal(body, &cs); err != nil {
			return nil, err
		}
		return cs, nil
	}
	cases := []Case{
		{Name: "unfiltered_minute", Grain: "minute", Metric: "summary"},
		{Name: "unfiltered_minute_user", Grain: "minute", Metric: "summary", Unit: "user"},
		{Name: "unfiltered_hour", Grain: "hour", Metric: "summary"},
		{Name: "unfiltered_day", Grain: "day", Metric: "summary"},
	}
	if p := topValue(ctx, conn, db, "platform"); p != "" {
		cases = append(cases, Case{Name: "platform_" + p, Grain: "minute", Metric: "summary",
			Filters: []filters.Filter{{Dimension: "platform", Op: "eq", Value: p}}})
	}
	if c := topValue(ctx, conn, db, "country"); c != "" {
		cases = append(cases, Case{Name: "country_" + c, Grain: "hour", Metric: "summary",
			Filters: []filters.Filter{{Dimension: "country", Op: "eq", Value: c}}})
	}
	return cases, nil
}

func topValue(ctx context.Context, conn driver.Conn, db, col string) string {
	rows, err := chclient.QueryMaps(ctx, conn, fmt.Sprintf(
		"SELECT %s AS v FROM %s.session_active_segments GROUP BY v ORDER BY count() DESC LIMIT 1", col, db))
	if err != nil || len(rows) == 0 {
		return ""
	}
	v, _ := rows[0]["v"].(string)
	return v
}

func partsEvidence(ctx context.Context, conn driver.Conn, db string) any {
	rows, _ := chclient.QueryMaps(ctx, conn, fmt.Sprintf(
		`SELECT table, sum(rows) AS rows, count() AS parts
		 FROM system.parts WHERE database = '%s' AND active GROUP BY table ORDER BY table`, db))
	return rows
}

func queryLogEvidence(ctx context.Context, conn driver.Conn, ids []string) []map[string]any {
	if len(ids) == 0 {
		return nil
	}
	_ = conn.Exec(ctx, "SYSTEM FLUSH LOGS")
	inList := ""
	for i, id := range ids {
		if i > 0 {
			inList += ", "
		}
		inList += "'" + id + "'"
	}
	rows, _ := chclient.QueryMaps(ctx, conn, fmt.Sprintf(
		`SELECT query_id, read_rows, read_bytes, result_rows, memory_usage,
		        query_duration_ms
		 FROM system.query_log
		 WHERE type = 'QueryFinish' AND query_id IN (%s)
		 ORDER BY event_time DESC LIMIT %d`, inList, len(ids)))
	return rows
}

func grainOr(g string) concurrency.Grain {
	if g == "" {
		return concurrency.GrainMinute
	}
	return concurrency.Grain(g)
}
func metricOr(m string) concurrency.Metric {
	if m == "" {
		return concurrency.MetricSummary
	}
	return concurrency.Metric(m)
}

func numPtr(v any) *float64 {
	switch t := v.(type) {
	case float64:
		return &t
	case int64:
		f := float64(t)
		return &f
	}
	return nil
}
func fmtPtr(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *p)
}

func writeJSON(path string, v any) {
	f, err := os.Create(path)
	must(err, "create "+path)
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	must(enc.Encode(v), "encode "+path)
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}
