// build_user_segments merges session segments into user-level islands and loads
// user_active_segments + user_minute_deltas (session-independent concurrency).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/csvload"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/otelx"
	"github.com/prathmeshxdev/pulse/internal/segments"
	"github.com/prathmeshxdev/pulse/internal/users"
)

func main() {
	inPath := flag.String("in", "", "raw events CSV (optional — if empty, read session_active_segments from ClickHouse)")
	configPath := flag.String("config", "", "path to config.env")
	version := flag.Uint64("version", 1, "pipeline run version")
	watermarkStr := flag.String("watermark", "", "optional RFC3339 watermark when -in is set")
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
	rebuild := flag.Bool("rebuild", true, "idempotent partition swap (default true)")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: build_user_segments -dsn $CLICKHOUSE_DSN [-in raw.csv]")
		os.Exit(2)
	}

	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}

	ctx := context.Background()
	shutdown := otelx.InitCLI(ctx)
	defer func() { _ = shutdown(ctx) }()

	var sessionSegs []models.Segment
	var err error
	if *inPath != "" {
		sessionSegs, err = segmentsFromCSV(*inPath, cfg, *version, *watermarkStr)
	} else {
		conn, err2 := chclient.Connect(ctx, *dsn)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "connect: %v\n", err2)
			os.Exit(1)
		}
		sessionSegs, err = chclient.FetchAllSegments(ctx, conn, cfg.Database)
		conn.Close()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "session segments: %v\n", err)
		os.Exit(1)
	}

	conn, err := chclient.Connect(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := users.LoadClickHouse(ctx, conn, cfg.Database, sessionSegs, *version, *rebuild); err != nil {
		fmt.Fprintf(os.Stderr, "load user tables: %v\n", err)
		os.Exit(1)
	}
	userSegs, udeltas := users.BuildFromSessionSegments(sessionSegs, *version)
	fmt.Printf("user segments=%d user_deltas=%d (from %d session segments)\n",
		len(userSegs), len(udeltas), len(sessionSegs))
}

func segmentsFromCSV(path string, cfg config.Constants, version uint64, watermarkStr string) ([]models.Segment, error) {
	events, err := csvload.ReadCSV(path)
	if err != nil {
		return nil, err
	}
	wm := events[0].EventTimestamp
	for _, e := range events {
		if e.EventTimestamp.After(wm) {
			wm = e.EventTimestamp
		}
	}
	if watermarkStr != "" {
		t, err := time.Parse(time.RFC3339, watermarkStr)
		if err != nil {
			return nil, err
		}
		wm = t
	}
	return segments.NewBuilder(cfg, version).BuildAll(events, wm), nil
}
