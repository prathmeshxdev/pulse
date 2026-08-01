package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/concurrency"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/filters"
	"github.com/prathmeshxdev/pulse/internal/preflight"
)

type Server struct {
	cfg       config.ServerConfig
	ch        driver.Conn
	preflight *preflight.Executor
	mux       *http.ServeMux
}

func New(cfg config.ServerConfig, ch driver.Conn, rdb *redis.Client) *Server {
	pf := preflight.New(rdb, preflight.Config{
		Enabled:     cfg.PreflightEnabled,
		CacheTTL:    cfg.PreflightCacheTTL,
		LockTTL:     cfg.PreflightLockTTL,
		WaitTimeout: cfg.PreflightWait,
	})
	s := &Server{cfg: cfg, ch: ch, preflight: pf, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /ping", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/concurrency/chart", s.handleChart)
	s.mux.HandleFunc("GET /api/v1/schema/dimensions", s.handleDimensions)
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
	var body chartRequestBody
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

	out, err := preflight.Do(r.Context(), s.preflight, key, func(ctx context.Context) (result, error) {
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
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
