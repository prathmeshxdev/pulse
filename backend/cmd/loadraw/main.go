// loadraw bulk-inserts a Sony LIV raw-events CSV into raw_events over the
// native protocol — no clickhouse-client needed, so it works directly against
// ClickHouse Cloud with a secure DSN. Use -rebuild=false to append (e.g. the
// replay tail); -rebuild=true drops the covered day partitions first.
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
	"github.com/prathmeshxdev/pulse/internal/otelx"
)

func main() {
	inPath := flag.String("in", "", "input raw-events CSV")
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
	configPath := flag.String("config", "", "path to config.env")
	rebuild := flag.Bool("rebuild", true, "drop covered day partitions before insert (idempotent full load); false appends")
	flag.Parse()

	if *inPath == "" || *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: loadraw -in raw.csv -dsn <dsn> [-rebuild=false]")
		os.Exit(2)
	}
	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}

	events, err := csvload.ReadCSV(*inPath)
	must(err, "read csv")
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "no events")
		os.Exit(1)
	}

	ctx := context.Background()
	shutdown := otelx.InitCLI(ctx)
	defer func() { _ = shutdown(ctx) }()
	ctx, span := otelx.Start(ctx, "loadraw",
		otelx.StringAttr("input", *inPath),
		otelx.BoolAttr("rebuild", *rebuild),
	)
	defer span.End()

	conn, err := chclient.Connect(ctx, *dsn)
	must(err, "connect")
	defer conn.Close()

	if *rebuild {
		times := make([]time.Time, 0, len(events))
		for _, e := range events {
			times = append(times, e.EventTimestamp)
		}
		// Atomic per-day swap via staging — no empty-partition window.
		must(chclient.StageAndReplace(ctx, conn, cfg.Database, "raw_events",
			chclient.PartitionDays(times...), func(stg string) error {
				return chclient.InsertRawEvents(ctx, conn, stg, events)
			}), "replace raw_events partitions")
	} else {
		must(chclient.InsertRawEvents(ctx, conn, cfg.Database+".raw_events", events), "insert raw_events")
	}
	span.SetAttributes(otelx.Int64Attr("events", int64(len(events))))
	fmt.Printf("loaded %d raw events into %s.raw_events (rebuild=%v)\n", len(events), cfg.Database, *rebuild)
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}
