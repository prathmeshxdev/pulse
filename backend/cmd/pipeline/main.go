package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/otelx"
)

// pipeline applies DDL migrations and optional SYSTEM RELOAD DICTIONARY.
func main() {
	dsn := flag.String("dsn", envOr("CLICKHOUSE_DSN", "clickhouse://default:@localhost:9000/default"), "ClickHouse DSN")
	migrations := flag.String("migrations", "../clickhouse/migrations", "migrations directory")
	reloadDict := flag.Bool("reload-dict", false, "reload content_dict after migrations")
	dropDB := flag.Bool("drop", false, "drop sony_liv database before applying migrations (full recreate)")
	execSQL := flag.String("exec", "", "run a single SQL statement and exit (no migrations)")
	flag.Parse()

	ctx := context.Background()
	shutdown := otelx.InitCLI(ctx)
	defer func() { _ = shutdown(ctx) }()
	ctx, span := otelx.Start(ctx, "pipeline.migrate",
		otelx.BoolAttr("drop", *dropDB),
		otelx.BoolAttr("reload_dict", *reloadDict),
	)
	defer span.End()

	conn, err := chclient.Connect(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if *execSQL != "" {
		if err := execMulti(ctx, conn, *execSQL); err != nil {
			fmt.Fprintf(os.Stderr, "exec: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	cfg := config.DefaultConstants()
	if *dropDB {
		fmt.Printf("dropping database %s\n", cfg.Database)
		if err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+cfg.Database); err != nil {
			fmt.Fprintf(os.Stderr, "drop database: %v\n", err)
			os.Exit(1)
		}
	}

	entries, err := os.ReadDir(*migrations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrations: %v\n", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := *migrations + "/" + e.Name()
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("applying %s\n", e.Name())
		if err := conn.Exec(ctx, string(body)); err != nil {
			// ClickHouse multi-statement: split on ;\n
			if err2 := execMulti(ctx, conn, string(body)); err2 != nil {
				fmt.Fprintf(os.Stderr, "exec %s: %v (also: %v)\n", e.Name(), err, err2)
				os.Exit(1)
			}
		}
	}

	if *reloadDict {
		cfg := config.DefaultConstants()
		sql := fmt.Sprintf("SYSTEM RELOAD DICTIONARY %s.content_dict", cfg.Database)
		if err := conn.Exec(ctx, sql); err != nil {
			fmt.Fprintf(os.Stderr, "reload dict: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("dictionary reloaded")
	}
	fmt.Printf("done at %s\n", time.Now().UTC().Format(time.RFC3339))
}

func execMulti(ctx context.Context, conn interface {
	Exec(context.Context, string, ...any) error
}, body string) error {
	var stmt string
	for _, line := range splitStatements(body) {
		stmt = line
		if stmt == "" {
			continue
		}
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%w\nstmt: %s", err, stmt)
		}
	}
	return nil
}

func splitStatements(body string) []string {
	parts := []string{}
	cur := ""
	for _, line := range splitLines(body) {
		trim := trimSpace(line)
		if trim == "" || hasPrefix(trim, "--") {
			continue
		}
		cur += line + "\n"
		if hasSuffix(trim, ";") {
			parts = append(parts, trimSuffix(trimSpace(cur), ";"))
			cur = ""
		}
	}
	if trimSpace(cur) != "" {
		parts = append(parts, trimSpace(cur))
	}
	return parts
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// tiny helpers to avoid importing strings solely for clarity in this cmd
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func hasSuffix(s, p string) bool {
	return len(s) >= len(p) && s[len(s)-len(p):] == p
}

func trimSuffix(s, p string) string {
	if hasSuffix(s, p) {
		return s[:len(s)-len(p)]
	}
	return s
}
