package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/concurrency"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/filters"
	"github.com/prathmeshxdev/pulse/internal/otelx"
	"github.com/prathmeshxdev/pulse/internal/preflight"
)

type Server struct {
	cfg           config.ServerConfig
	ch            driver.Conn
	preflight     *preflight.Executor
	tracer        trace.Tracer
	otelShutdown  func(context.Context) error
	mux           *http.ServeMux
}

func New(cfg config.ServerConfig, ch driver.Conn, rdb *redis.Client) *Server {
	pf := preflight.New(rdb, preflight.Config{
		Enabled:     cfg.PreflightEnabled,
		CacheTTL:    cfg.PreflightCacheTTL,
		LockTTL:     cfg.PreflightLockTTL,
		WaitTimeout: cfg.PreflightWait,
	})
	tracer, shutdown, _ := otelx.Setup(context.Background())
	s := &Server{cfg: cfg, ch: ch, preflight: pf, tracer: tracer, otelShutdown: shutdown, mux: http.NewServeMux()}
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
	q, err := concurrency.BuildChartQuery(req, s.cfg.Constants.Database, s.cfg.Constants.MaxSegmentSpanHours)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	key := preflight.KeyFromString(q.CacheKey)
	type result struct {
		SQL     string           `json:"sql,omitempty"`
		Rows    []map[string]any `json:"rows"`
		Peak    any              `json:"peak,omitempty"`
		Avg     any              `json:"avg,omitempty"`
		CacheKey string          `json:"cache_key"`
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
	// Expose SQL only when explicitly requested (debug).
	if r.URL.Query().Get("debug") == "1" {
		out.SQL = q.SQL
	}
	writeJSON(w, http.StatusOK, out)
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
