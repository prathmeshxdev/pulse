package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/concurrency"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/filters"
	"github.com/prathmeshxdev/pulse/internal/livestate"
	"github.com/prathmeshxdev/pulse/internal/otelx"
	"github.com/prathmeshxdev/pulse/internal/preflight"
)

type Server struct {
	cfg          config.ServerConfig
	ch           driver.Conn
	preflight    *preflight.Executor
	live         *livestate.Store // nil if LiveEnabled=false or redis unavailable
	tracer       trace.Tracer
	otelShutdown func(context.Context) error
	mux          *http.ServeMux
}

func New(cfg config.ServerConfig, ch driver.Conn, rdb *redis.Client) *Server {
	pf := preflight.New(rdb, preflight.Config{
		Enabled:     cfg.PreflightEnabled,
		CacheTTL:    cfg.PreflightCacheTTL,
		LockTTL:     cfg.PreflightLockTTL,
		WaitTimeout: cfg.PreflightWait,
	})
	var live *livestate.Store
	if cfg.LiveEnabled && rdb != nil {
		live = livestate.New(rdb, cfg.Constants, cfg.LiveTTL)
	}
	tracer, shutdown, _ := otelx.Setup(context.Background())
	s := &Server{cfg: cfg, ch: ch, preflight: pf, live: live, tracer: tracer, otelShutdown: shutdown, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

// Shutdown flushes the OTel exporter (no-op when disabled).
func (s *Server) Shutdown(ctx context.Context) error {
	if s.otelShutdown != nil {
		return s.otelShutdown(ctx)
	}
	return nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /ping", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/concurrency/chart", s.handleChart)
	s.mux.HandleFunc("POST /api/v1/concurrency/breakdown", s.handleBreakdown)
	s.mux.HandleFunc("GET /api/v1/concurrency/live", s.handleLive)
	s.mux.HandleFunc("GET /api/v1/schema/dimensions", s.handleDimensions)
	s.mux.HandleFunc("GET /api/v1/schema/values", s.handleValues)
	s.mux.HandleFunc("GET /api/v1/schema/window", s.handleWindow)
}

// handleWindow returns the served time range so the UI can default its picker.
func (s *Server) handleWindow(w http.ResponseWriter, r *http.Request) {
	db := s.cfg.Constants.Database
	rows, err := chclient.QueryMaps(r.Context(), s.ch,
		"SELECT min(minute) AS start, max(minute) + toIntervalMinute(1) AS end FROM "+db+".minute_deltas")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]any{"start": nil, "end": nil}
	if len(rows) == 1 {
		out["start"] = rows[0]["start"]
		out["end"] = rows[0]["end"]
	}
	writeJSON(w, http.StatusOK, out)
}

// handleValues returns distinct values for a filterable dimension (for the UI
// filter dropdowns). Capped; segment dims read from session_active_segments,
// content dims from content_metadata.
func (s *Server) handleValues(w http.ResponseWriter, r *http.Request) {
	dim := r.URL.Query().Get("dimension")
	kind, ref, ok := filters.Lookup(dim)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown dimension: "+dim)
		return
	}
	db := s.cfg.Constants.Database
	var sql string
	switch kind {
	case "segment":
		sql = "SELECT DISTINCT " + ref + " AS v FROM " + db + ".session_active_segments WHERE v != '' ORDER BY v LIMIT 500"
	default: // dict → content_metadata column
		sql = "SELECT DISTINCT " + ref + " AS v FROM " + db + ".content_metadata WHERE v != '' ORDER BY v LIMIT 500"
	}
	rows, err := chclient.QueryMaps(r.Context(), s.ch, sql)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	vals := make([]any, 0, len(rows))
	for _, row := range rows {
		vals = append(vals, row["v"])
	}
	writeJSON(w, http.StatusOK, map[string]any{"dimension": dim, "values": vals})
}

// handleLive returns real-time "active viewers now". Two sources are available:
//
//   - Redis (internal/livestate), when streamd is running: EXACT — the same
//     state machine as batch (TestStreamingMatchesBatch), maintained
//     incrementally per event with a sliding 48h TTL for late corrections.
//     O(1) count via an active-session set. This is now the primary source.
//   - ClickHouse session_live_state MV: an approximation via argMax/max
//     reconstruction (measured ~0.5% off exact — 637 vs 640 at a validated
//     instant). Kept as a fallback for when streamd/Redis isn't running, since
//     it requires no separate process.
//
// ?source=redis|mv forces one explicitly (mainly for side-by-side validation);
// default is redis when available, else mv.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "mv"
		if s.live != nil {
			source = "redis"
		}
	}
	if source == "redis" {
		if s.live == nil {
			writeErr(w, http.StatusServiceUnavailable, "redis live source not configured (LIVE_ENABLED/redis)")
			return
		}
		s.handleLiveRedis(w, r)
		return
	}
	s.handleLiveMV(w, r)
}

// handleLiveRedis serves the exact live count from the Redis active-session set.
func (s *Server) handleLiveRedis(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// The active set is only touched on writes; without a periodic sweep a
	// session that goes silent past the heartbeat grace with no closing
	// event would read as falsely-active until its own next event arrives
	// (caught by cmd/validateredis on real data). Throttled so concurrent
	// requests don't turn this into an O(active sessions) scan per call.
	if _, err := s.live.SweepIfDue(ctx, time.Now(), 5*time.Second); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := s.live.ActiveCount(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":     "redis",
		"active_now": n,
		"exact":      true,
	})
}

// handleLiveMV serves the approximate live count from the ClickHouse
// session_live_state materialized view (argMax/max reconstruction).
func (s *Server) handleLiveMV(w http.ResponseWriter, r *http.Request) {
	db := s.cfg.Constants.Database
	grace := s.cfg.Constants.HeartbeatGraceSec
	by := r.URL.Query().Get("by")

	// Merge per-session state, then apply the active predicate at watermark T.
	base := "SELECT video_session_id, maxMerge(closed) AS closed, argMaxMerge(fg) AS fg, " +
		"argMaxMerge(playing) AS playing, maxIfMerge(last_hb) AS last_hb, " +
		"argMaxMerge(platform) AS platform, argMaxMerge(country) AS country " +
		"FROM " + db + ".session_live_state GROUP BY video_session_id"
	active := fmt.Sprintf("NOT closed AND fg=1 AND playing=1 AND dateDiff('second', last_hb, T) <= %d", grace)
	twith := "WITH (SELECT max(event_timestamp) FROM " + db + ".raw_events) AS T "

	var sql string
	if by == "platform" || by == "country" {
		sql = twith + "SELECT " + by + " AS value, countIf(" + active + ") AS active FROM (" + base +
			") GROUP BY value HAVING active > 0 ORDER BY active DESC LIMIT 20 FORMAT JSONEachRow"
	} else {
		sql = twith + "SELECT countIf(" + active + ") AS active_now, countIf(NOT closed) AS open_sessions FROM (" +
			base + ") FORMAT JSONEachRow"
	}
	rows, err := chclient.QueryMaps(r.Context(), s.ch, sql)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if by != "" {
		writeJSON(w, http.StatusOK, map[string]any{"source": "mv", "by": by, "rows": rows})
		return
	}
	out := map[string]any{"source": "mv", "active_now": 0, "open_sessions": 0, "exact": false}
	if len(rows) == 1 {
		out["active_now"] = rows[0]["active_now"]
		out["open_sessions"] = rows[0]["open_sessions"]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDimensions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"dimensions": filters.Dimensions(),
		"database":   s.cfg.Constants.Database,
	})
}

type chartRequestBody struct {
	Start   string           `json:"start"`
	End     string           `json:"end"`
	Grain   string           `json:"grain"`
	Metric  string           `json:"metric"`
	Filters []filters.Filter `json:"filters"`
	Engine  string           `json:"engine"` // "" | "narrow" (default) | "rollup"
}

func (s *Server) handleChart(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.tracer.Start(r.Context(), "concurrency.chart")
	defer span.End()

	var body chartRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		span.SetStatus(codes.Error, "bad json")
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	span.SetAttributes(
		otelx.StringAttr("grain", body.Grain),
		otelx.StringAttr("metric", body.Metric),
		otelx.IntAttr("filters", len(body.Filters)),
	)
	start, err := parseTime(body.Start)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid start: "+err.Error())
		return
	}
	end, err := parseTime(body.End)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid end: "+err.Error())
		return
	}

	req := concurrency.Request{
		Start:   start,
		End:     end,
		Grain:   concurrency.Grain(body.Grain),
		Metric:  concurrency.Metric(body.Metric),
		Filters: body.Filters,
	}
	// Engine: narrow (default, semi-join) or the opt-in wide rollup. Rollup is used
	// only when requested AND all filters are rollup dimensions; otherwise we fall
	// back to narrow so the request never fails on an unsupported filter.
	engine := "narrow"
	var q concurrency.Query
	if body.Engine == "rollup" && filters.RollupSupported(req.Filters) {
		q, err = concurrency.BuildRollupQuery(req, s.cfg.Constants.Database, s.cfg.Constants.MaxSegmentSpanHours)
		engine = "rollup"
	} else {
		q, err = concurrency.BuildChartQuery(req, s.cfg.Constants.Database, s.cfg.Constants.MaxSegmentSpanHours)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	span.SetAttributes(otelx.StringAttr("engine", engine))

	key := preflight.KeyFromString(q.CacheKey)
	type result struct {
		SQL      string           `json:"sql,omitempty"`
		Rows     []map[string]any `json:"rows"`
		Peak     any              `json:"peak,omitempty"`
		Avg      any              `json:"avg,omitempty"`
		CacheKey string           `json:"cache_key"`
		Engine   string           `json:"engine"`
	}

	out, err := preflight.Do(ctx, s.preflight, key, func(ctx context.Context) (result, error) {
		rows, err := chclient.QueryMaps(ctx, s.ch, q.SQL)
		if err != nil {
			return result{}, err
		}
		res := result{Rows: rows, CacheKey: key}
		if len(rows) == 1 {
			if v, ok := rows[0]["peak_concurrency"]; ok {
				res.Peak = v
			}
			if v, ok := rows[0]["avg_concurrency"]; ok {
				res.Avg = v
			}
		}
		return res, nil
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	span.SetAttributes(otelx.IntAttr("result_rows", len(out.Rows)))
	out.Engine = engine
	// Expose SQL only when explicitly requested (debug).
	if r.URL.Query().Get("debug") == "1" {
		out.SQL = q.SQL
	}
	writeJSON(w, http.StatusOK, out)
}

type breakdownBody struct {
	Start     string           `json:"start"`
	End       string           `json:"end"`
	Grain     string           `json:"grain"`
	Dimension string           `json:"dimension"`
	Filters   []filters.Filter `json:"filters"`
	Limit     int              `json:"limit"`
}

// handleBreakdown computes peak+avg concurrency per value of a dimension (top-N
// by segment count). Each value reuses the exact normative summary template, so
// a breakdown row equals what you'd get by filtering to that value — and it
// surfaces the problem's point that different dimension values peak at different
// times/heights.
func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.tracer.Start(r.Context(), "concurrency.breakdown")
	defer span.End()

	var body breakdownBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	start, err := parseTime(body.Start)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid start: "+err.Error())
		return
	}
	end, err := parseTime(body.End)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid end: "+err.Error())
		return
	}
	kind, ref, ok := filters.Lookup(body.Dimension)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown dimension: "+body.Dimension)
		return
	}
	limit := body.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	db := s.cfg.Constants.Database
	span.SetAttributes(otelx.StringAttr("dimension", body.Dimension), otelx.IntAttr("filters", len(body.Filters)))

	// Value expression: typed column, or dictGet for content attributes.
	valueExpr := ref
	if kind == "dict" {
		valueExpr = "dictGet('" + db + ".content_dict', '" + ref + "', content_id)"
	}
	// Respect any existing filters when picking the top-N values.
	preds, hasFilters, err := filters.BuildSegmentPredicates(body.Filters, db)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	where := "WHERE " + valueExpr + " != ''"
	if hasFilters {
		where += " AND " + strings.Join(preds, " AND ")
	}
	valSQL := "SELECT " + valueExpr + " AS v FROM " + db + ".session_active_segments FINAL " + where +
		" GROUP BY v ORDER BY count() DESC LIMIT " + strconv.Itoa(limit)
	valRows, err := chclient.QueryMaps(ctx, s.ch, valSQL)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	type brow struct {
		Value string   `json:"value"`
		Peak  *float64 `json:"peak"`
		Avg   *float64 `json:"avg"`
	}
	out := make([]brow, len(valRows))
	sem := make(chan struct{}, 6) // bounded fan-out
	var wg sync.WaitGroup
	for i, vr := range valRows {
		v, _ := vr["v"].(string)
		out[i] = brow{Value: v}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, v string) {
			defer wg.Done()
			defer func() { <-sem }()
			req := concurrency.Request{
				Start: start, End: end,
				Grain:  concurrency.Grain(body.Grain),
				Metric: concurrency.MetricSummary,
				Filters: append(append([]filters.Filter{}, body.Filters...),
					filters.Filter{Dimension: body.Dimension, Op: "eq", Value: v}),
			}
			q, err := concurrency.BuildChartQuery(req, db, s.cfg.Constants.MaxSegmentSpanHours)
			if err != nil {
				return
			}
			rows, err := chclient.QueryMaps(ctx, s.ch, q.SQL)
			if err != nil || len(rows) != 1 {
				return
			}
			out[i].Peak = numToPtr(rows[0]["peak_concurrency"])
			out[i].Avg = numToPtr(rows[0]["avg_concurrency"])
		}(i, v)
	}
	wg.Wait()
	// Select top-N by count, present sorted by peak (nils last).
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := -1.0, -1.0
		if out[i].Peak != nil {
			pi = *out[i].Peak
		}
		if out[j].Peak != nil {
			pj = *out[j].Peak
		}
		return pi > pj
	})
	writeJSON(w, http.StatusOK, map[string]any{"dimension": body.Dimension, "rows": out})
}

func numToPtr(v any) *float64 {
	switch t := v.(type) {
	case float64:
		return &t
	case int64:
		f := float64(t)
		return &f
	case uint64:
		f := float64(t)
		return &f
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	var err error
	for _, f := range formats {
		var t time.Time
		t, err = time.Parse(f, s)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
