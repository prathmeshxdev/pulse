package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/csvload"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/otelx"
	"github.com/prathmeshxdev/pulse/internal/segments"
	"github.com/prathmeshxdev/pulse/internal/users"
)

// build_segments reads raw events (CSV or JSONL) and emits segments + deltas as JSONL.
// This is the independent Go reference path (FINAL_PLAN Phase 1 / ACTIVE_INTERVAL_LOGIC §3c).
func main() {
	inPath := flag.String("in", "", "input CSV path (Sony LIV raw events)")
	outSeg := flag.String("segments", "segments.jsonl", "output segments JSONL (empty to skip)")
	outDelta := flag.String("deltas", "deltas.jsonl", "output deltas JSONL (empty to skip)")
	configPath := flag.String("config", "", "path to config.env")
	version := flag.Uint64("version", 1, "pipeline run version")
	watermarkStr := flag.String("watermark", "", "optional RFC3339 watermark; default = max event ts")
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN; if set, insert segments+deltas into ClickHouse")
	rebuild := flag.Bool("rebuild", true, "drop affected partitions before insert (idempotent load)")
	flag.Parse()

	if *inPath == "" {
		fmt.Fprintln(os.Stderr, "usage: build_segments -in raw.csv")
		os.Exit(2)
	}

	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}

	events, err := csvload.ReadCSV(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "no events")
		os.Exit(1)
	}

	wm := events[0].EventTimestamp
	for _, e := range events {
		if e.EventTimestamp.After(wm) {
			wm = e.EventTimestamp
		}
	}
	if *watermarkStr != "" {
		t, err := time.Parse(time.RFC3339, *watermarkStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "watermark: %v\n", err)
			os.Exit(1)
		}
		wm = t
	}

	b := segments.NewBuilder(cfg, *version)
	segs := b.BuildAll(events, wm)
	drows := deltas.EmitAll(segs)

	if *outSeg != "" {
		if err := writeJSONL(*outSeg, segs); err != nil {
			fmt.Fprintf(os.Stderr, "segments: %v\n", err)
			os.Exit(1)
		}
	}
	if *outDelta != "" {
		if err := writeJSONL(*outDelta, drows); err != nil {
			fmt.Fprintf(os.Stderr, "deltas: %v\n", err)
			os.Exit(1)
		}
	}

	if *dsn != "" {
		ctx := context.Background()
		shutdown := otelx.InitCLI(ctx)
		defer func() { _ = shutdown(ctx) }()
		ctx, span := otelx.Start(ctx, "build_segments",
			otelx.StringAttr("input", *inPath),
			otelx.BoolAttr("rebuild", *rebuild),
		)
		defer span.End()

		if err := loadClickHouse(ctx, *dsn, cfg.Database, segs, drows, *version, *rebuild); err != nil {
			fmt.Fprintf(os.Stderr, "clickhouse load: %v\n", err)
			os.Exit(1)
		}
		span.SetAttributes(
			otelx.Int64Attr("events", int64(len(events))),
			otelx.Int64Attr("segments", int64(len(segs))),
			otelx.Int64Attr("deltas", int64(len(drows))),
		)
		fmt.Printf("clickhouse: inserted %d segments + %d deltas into %s\n", len(segs), len(drows), cfg.Database)
	}

	fmt.Printf("events=%d segments=%d deltas=%d watermark=%s\n",
		len(events), len(segs), len(drows), wm.UTC().Format(time.RFC3339Nano))
}

// loadClickHouse inserts segments + deltas. With rebuild (default) each affected
// day is swapped atomically via REPLACE PARTITION FROM a staging table, so the
// load is idempotent AND never exposes an empty partition to concurrent queries.
// With rebuild=false it appends (segments dedup by ReplacingMergeTree version;
// deltas would double on a rerun — only use for genuinely new data).
func loadClickHouse(ctx context.Context, dsn, database string, segs []models.Segment, drows []models.MinuteDelta, version uint64, rebuild bool) error {
	conn, err := chclient.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close()

	if !rebuild {
		if err := chclient.InsertSegments(ctx, conn, database+".session_active_segments", segs); err != nil {
			return err
		}
		return chclient.InsertDeltas(ctx, conn, database+".minute_deltas", drows)
	}

	segTimes := make([]time.Time, 0, len(segs))
	for _, s := range segs {
		segTimes = append(segTimes, s.SegmentStart)
	}
	deltaTimes := make([]time.Time, 0, len(drows))
	for _, d := range drows {
		deltaTimes = append(deltaTimes, d.Minute)
	}
	if err := chclient.StageAndReplace(ctx, conn, database, "session_active_segments",
		chclient.PartitionDays(segTimes...), func(stg string) error {
			return chclient.InsertSegments(ctx, conn, stg, segs)
		}); err != nil {
		return err
	}
	if err := chclient.StageAndReplace(ctx, conn, database, "minute_deltas",
		chclient.PartitionDays(deltaTimes...), func(stg string) error {
			return chclient.InsertDeltas(ctx, conn, stg, drows)
		}); err != nil {
		return err
	}

	// Optional wide rollup — populated by the same pipeline when the table exists
	// (migration 008). Same any-overlap edges, dimensions denormalized. Idempotent
	// via the same staging swap on minute-day partitions.
	if chclient.TableExists(ctx, conn, database, "concurrency_minute_serving") {
		wide := deltas.EmitAllWide(segs)
		wideTimes := make([]time.Time, 0, len(wide))
		for _, wd := range wide {
			wideTimes = append(wideTimes, wd.Minute)
		}
		if err := chclient.StageAndReplace(ctx, conn, database, "concurrency_minute_serving",
			chclient.PartitionDays(wideTimes...), func(stg string) error {
				return chclient.InsertRollup(ctx, conn, stg, wide)
			}); err != nil {
			return err
		}
		fmt.Printf("clickhouse: populated concurrency_minute_serving rollup (%d wide deltas)\n", len(wide))
	}
	if err := users.LoadClickHouse(ctx, conn, database, segs, version, rebuild); err != nil {
		return fmt.Errorf("user concurrency: %w", err)
	}
	return nil
}

func writeJSONL[T any](path string, rows []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}
