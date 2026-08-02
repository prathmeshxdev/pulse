package chclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Connect opens a native ClickHouse connection from a DSN like
// clickhouse://user:pass@host:9000/database
func Connect(ctx context.Context, dsn string) (driver.Conn, error) {
	opts, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	// Cloud services sleep when idle; the first connect wakes them and can take
	// 10-30s, so allow a generous ping window.
	pingCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse ping: %w", err)
	}
	return conn, nil
}

func parseDSN(dsn string) (*clickhouse.Options, error) {
	// Allow bare host:port/db forms by normalising.
	if !strings.Contains(dsn, "://") {
		dsn = "clickhouse://" + dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	db := strings.TrimPrefix(u.Path, "/")
	if db == "" {
		db = "sony_liv"
	}
	pass, _ := u.User.Password()
	addr := u.Host
	if addr == "" {
		addr = "localhost:9000"
	}
	q := u.Query()
	// Protocol: native by default; HTTP when asked, or when the port is the
	// ClickHouse HTTP(S) port. ClickHouse Cloud often exposes only 8443 (HTTPS)
	// and firewalls the native 9440, so HTTP is the reliable Cloud transport.
	proto := clickhouse.Native
	port := ""
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		port = addr[i+1:]
	}
	switch {
	case q.Get("protocol") == "http", u.Scheme == "http", u.Scheme == "https", port == "8443", port == "8123":
		proto = clickhouse.HTTP
	}
	opts := &clickhouse.Options{
		Addr:     []string{addr},
		Protocol: proto,
		Auth: clickhouse.Auth{
			Database: db,
			Username: u.User.Username(),
			Password: pass,
		},
		Settings: clickhouse.Settings{
			// Bulk load (7M+ rows) and segment rebuilds need headroom; chart
			// queries finish well under this. Override per-query when needed.
			"max_execution_time": 600,
			// session_active_segments is partitioned by toYYYYMMDD(segment_start)
			// and its ReplacingMergeTree key is segment_id, which is derived from
			// segment_start — so every version of a key lives in one partition.
			// That precondition makes per-partition FINAL exact, so we let it skip
			// the cross-partition merge (verified: 0 segment_ids span >1 partition,
			// FINAL count and peak unchanged). Harmless for the other tables.
			"do_not_merge_across_partitions_select_final": 1,
			// Required for native JSON column read/write on ClickHouse Cloud 25.3+.
			"output_format_native_use_flattened_dynamic_and_json_serialization": 1,
		},
		DialTimeout: 30 * time.Second,
	}
	// TLS on for secure=true, an https scheme, or the HTTPS/native-secure ports.
	if q.Get("secure") == "true" || u.Scheme == "https" || port == "8443" || port == "9440" {
		opts.TLS = &tls.Config{InsecureSkipVerify: q.Get("skip_verify") == "true"}
	}
	if opts.Auth.Username == "" {
		opts.Auth.Username = "default"
	}
	return opts, nil
}

// QueryMaps runs a SQL string and returns rows as maps (for chart responses).
func QueryMaps(ctx context.Context, conn driver.Conn, sql string) ([]map[string]any, error) {
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := rows.Columns()
	types := rows.ColumnTypes()
	out := make([]map[string]any, 0)
	for rows.Next() {
		ptrs := make([]any, len(cols))
		holders := make([]any, len(cols))
		for i := range cols {
			holders[i] = scanTarget(types[i].DatabaseTypeName())
			ptrs[i] = holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = deref(holders[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func scanTarget(dbType string) any {
	dt := strings.ToLower(dbType)
	switch {
	case strings.Contains(dt, "datetime"):
		return new(time.Time)
	case strings.Contains(dt, "float"):
		return new(float64)
	case strings.Contains(dt, "uint"): // must precede "int" — "uint64" contains "int"
		return new(uint64)
	case strings.Contains(dt, "int"):
		return new(int64)
	default:
		return new(string)
	}
}

func deref(v any) any {
	switch t := v.(type) {
	case *time.Time:
		if t == nil {
			return nil
		}
		return *t
	case *float64:
		if t == nil {
			return nil
		}
		return *t
	case *int64:
		if t == nil {
			return nil
		}
		return *t
	case *uint64:
		if t == nil {
			return nil
		}
		return *t
	case *string:
		if t == nil {
			return nil
		}
		return *t
	default:
		return v
	}
}
