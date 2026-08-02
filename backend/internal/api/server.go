package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

	propTypesMu  sync.RWMutex
	propTypes    filters.PropertyTypes
	propTypesAt  time.Time
	propTypesTTL time.Duration
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
	s := &Server{
		cfg: cfg, ch: ch, preflight: pf, live: live, tracer: tracer, otelShutdown: shutdown, mux: http.NewServeMux(),
		propTypesTTL: 5 * time.Minute,
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ctx, span := s.tracer.Start(r.Context(), "http."+r.Method+" "+r.URL.Path,
			trace.WithAttributes(
				otelx.StringAttr("http.method", r.Method),
				otelx.StringAttr("http.route", r.URL.Path),
			),
		)
		defer span.End()
		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		s.mux.ServeHTTP(rw, r.WithContext(ctx))
		var err error
		if rw.code >= 400 {
			err = fmt.Errorf("http %d", rw.code)
		}
		// Request-level metrics/logs for every route (chart/breakdown add richer child spans).
		otelx.ObserveRequest(ctx, span, r.URL.Path, started, err,
			otelx.IntAttr("http.status_code", rw.code),
			otelx.StringAttr("http.method", r.Method),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

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

func (s *Server) propertyTypesResolver(ctx context.Context) filters.PropertyTypeResolver {
	s.propTypesMu.RLock()
	if s.propTypes != nil && time.Since(s.propTypesAt) < s.propTypesTTL {
		pt := s.propTypes
		s.propTypesMu.RUnlock()
		return filters.StringFallbackTypes{PropertyTypes: pt}
	}
	s.propTypesMu.RUnlock()

	types, err := chclient.FetchPropertyKeyTypes(ctx, s.ch, s.cfg.Constants.Database)
	if err != nil || types == nil {
		return filters.StringFallbackTypes{}
	}
	s.propTypesMu.Lock()
	s.propTypes = types
	s.propTypesAt = time.Now()
	s.propTypesMu.Unlock()
	return filters.StringFallbackTypes{PropertyTypes: types}
}

// handleValues returns distinct values for a filterable dimension (for the UI
// filter dropdowns). Works for typed segment columns, content_dict attributes,
// and dynamic properties keys (via typed toString expressions).
func (s *Server) handleValues(w http.ResponseWriter, r *http.Request) {
	dim := r.URL.Query().Get("dimension")
	propTypes := s.propertyTypesResolver(r.Context())
	resolved, ok := filters.ResolveDimension(dim, s.cfg.Constants.Database, propTypes)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown dimension: "+dim)
		return
	}
	db := s.cfg.Constants.Database
	expr := filters.ValueSuggestionExpr(resolved, db, propTypes)
	nonEmpty := filters.NonEmptyPredicate(resolved, expr)
	var sql string
	switch resolved.Kind {
	case "dict":
		sql = fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s.content_metadata WHERE %s ORDER BY v LIMIT 500", expr, db, nonEmpty)
	default:
		sql = fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s.session_active_segments FINAL WHERE %s ORDER BY v LIMIT 500", expr, db, nonEmpty)
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

func (s *Server) handleDimensions(w http.ResponseWriter, r *http.Request) {
	propTypes := s.propertyTypesResolver(r.Context())
	dims := filters.StaticDimensions()
	if dynamic := filters.PropertyDimensions(asPropertyTypes(propTypes)); len(dynamic) > 0 {
		dims = append(dims, dynamic...)
		sort.Slice(dims, func(i, j int) bool { return dims[i].Name < dims[j].Name })
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dimensions": dims,
		"database":   s.cfg.Constants.Database,
	})
}

func asPropertyTypes(r filters.PropertyTypeResolver) filters.PropertyTypes {
	if pt, ok := r.(filters.StringFallbackTypes); ok {
		return pt.PropertyTypes
	}
	if pt, ok := r.(filters.PropertyTypes); ok {
		return pt
	}
	return nil
}

type chartRequestBody struct {
	Start   string           `json:"start"`
	End     string           `json:"end"`
	Grain   string           `json:"grain"`
	Metric  string           `json:"metric"`
	Unit    string           `json:"unit"` // "" | "session" | "user"
	Filters []filters.Filter `json:"filters"`
}

func (s *Server) handleChart(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	ctx, span := s.tracer.Start(r.Context(), "concurrency.chart")
	defer span.End()

	var body chartRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		span.SetStatus(codes.Error, "bad json")
		otelx.Error(ctx, "chart bad json", otelx.StringAttr("error.message", err.Error()))
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
		otelx.Error(ctx, "chart invalid start", otelx.StringAttr("error.message", err.Error()))
		writeErr(w, http.StatusBadRequest, "invalid start: "+err.Error())
		return
	}
	end, err := parseTime(body.End)
	if err != nil {
		otelx.Error(ctx, "chart invalid end", otelx.StringAttr("error.message", err.Error()))
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
	if body.Unit != "" {
		req.Unit = concurrency.ParseCountUnit(body.Unit)
	} else {
		req.Unit = concurrency.ParseCountUnit(s.cfg.Constants.DefaultCountUnit())
	}
	span.SetAttributes(otelx.StringAttr("unit", string(req.Unit)))
	q, err := concurrency.BuildChartQuery(req, s.cfg.Constants.Database, s.cfg.Constants.MaxSegmentSpanHours, s.propertyTypesResolver(ctx))
	if err != nil {
		otelx.Error(ctx, "chart build query failed", otelx.StringAttr("error.message", err.Error()))
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	key := preflight.KeyFromString(q.CacheKey)
	type result struct {
		SQL      string           `json:"sql,omitempty"`
		Rows     []map[string]any `json:"rows"`
		Peak     any              `json:"peak,omitempty"`
		Avg      any              `json:"avg,omitempty"`
		CacheKey string           `json:"cache_key"`
	}

	out, err := preflight.Do(ctx, s.preflight, key, func(ctx context.Context) (result, error) {
		qStart := time.Now()
		rows, err := chclient.QueryMaps(ctx, s.ch, q.SQL)
		otelx.ObserveQuery(ctx, span, "chart", qStart, err)
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
		otelx.Error(ctx, "chart query failed", otelx.StringAttr("error.message", err.Error()))
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	span.SetAttributes(
		otelx.IntAttr("result_rows", len(out.Rows)),
		otelx.StringAttr("cache_key", out.CacheKey),
	)
	if out.Peak != nil {
		span.SetAttributes(otelx.FloatAttr("peak", toFloat64(out.Peak)))
	}
	if out.Avg != nil {
		span.SetAttributes(otelx.FloatAttr("avg", toFloat64(out.Avg)))
	}
	otelx.Info(ctx, "chart ok",
		otelx.StringAttr("grain", body.Grain),
		otelx.StringAttr("unit", string(req.Unit)),
		otelx.IntAttr("filters", len(body.Filters)),
		otelx.IntAttr("result_rows", len(out.Rows)),
		otelx.FloatAttr("peak", toFloat64(out.Peak)),
		otelx.FloatAttr("duration_ms", float64(time.Since(started).Milliseconds())),
	)
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
	Unit      string           `json:"unit"`
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
	started := time.Now()
	ctx, span := s.tracer.Start(r.Context(), "concurrency.breakdown")
	defer span.End()

	var body breakdownBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		otelx.Error(ctx, "breakdown bad json", otelx.StringAttr("error.message", err.Error()))
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
	db := s.cfg.Constants.Database
	propTypes := s.propertyTypesResolver(ctx)
	resolved, ok := filters.ResolveDimension(body.Dimension, db, propTypes)
	if !ok {
		otelx.Error(ctx, "breakdown unknown dimension", otelx.StringAttr("dimension", body.Dimension))
		writeErr(w, http.StatusBadRequest, "unknown dimension: "+body.Dimension)
		return
	}
	limit := body.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	unit := concurrency.ParseCountUnit(s.cfg.Constants.DefaultCountUnit())
	if body.Unit != "" {
		unit = concurrency.ParseCountUnit(body.Unit)
	}
	span.SetAttributes(
		otelx.StringAttr("dimension", body.Dimension),
		otelx.StringAttr("unit", string(unit)),
		otelx.IntAttr("filters", len(body.Filters)),
		otelx.IntAttr("limit", limit),
	)

	valueExpr := filters.BreakdownValueExpr(resolved, db, propTypes)
	preds, hasFilters, err := filters.BuildSegmentPredicates(body.Filters, db, propTypes)
	if err != nil {
		otelx.Error(ctx, "breakdown bad filters", otelx.StringAttr("error.message", err.Error()))
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	nonEmpty := filters.NonEmptyPredicate(resolved, valueExpr)
	where := "WHERE " + nonEmpty
	if hasFilters {
		where += " AND " + strings.Join(preds, " AND ")
	}
	segTable := concurrency.SegmentTable(unit)
	valSQL := fmt.Sprintf("SELECT %s AS v FROM %s.%s FINAL %s GROUP BY v ORDER BY count() DESC LIMIT %d",
		valueExpr, db, segTable, where, limit)
	qStart := time.Now()
	valRows, err := chclient.QueryMaps(ctx, s.ch, valSQL)
	otelx.ObserveQuery(ctx, span, "breakdown.values", qStart, err)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		otelx.Error(ctx, "breakdown values query failed", otelx.StringAttr("error.message", err.Error()))
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
				Grain:   concurrency.Grain(body.Grain),
				Metric:  concurrency.MetricSummary,
				Unit:    unit,
				Filters: append(append([]filters.Filter{}, body.Filters...),
					filters.Filter{Dimension: body.Dimension, Op: "eq", Value: v}),
			}
			q, err := concurrency.BuildChartQuery(req, db, s.cfg.Constants.MaxSegmentSpanHours, propTypes)
			if err != nil {
				return
			}
			qs := time.Now()
			rows, err := chclient.QueryMaps(ctx, s.ch, q.SQL)
			otelx.ObserveQuery(ctx, span, "breakdown.cell", qs, err)
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
	span.SetAttributes(otelx.IntAttr("result_rows", len(out)))
	otelx.Info(ctx, "breakdown ok",
		otelx.StringAttr("dimension", body.Dimension),
		otelx.StringAttr("unit", string(unit)),
		otelx.IntAttr("result_rows", len(out)),
		otelx.FloatAttr("duration_ms", float64(time.Since(started).Milliseconds())),
	)
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

func toFloat64(v any) float64 {
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
	case *float64:
		if t == nil {
			return 0
		}
		return *t
	default:
		return 0
	}
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
