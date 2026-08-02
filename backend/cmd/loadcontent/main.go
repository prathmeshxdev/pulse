// loadcontent bulk-inserts the content metadata CSV into content_metadata and
// reloads content_dict — pure Go over the native protocol (no clickhouse-client),
// so it works against ClickHouse Cloud.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/csvload"
)

func main() {
	inPath := flag.String("in", "", "content metadata CSV (content_id,title,video_type,category[,show_name])")
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
	configPath := flag.String("config", "", "path to config.env")
	flag.Parse()

	if *inPath == "" || *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: loadcontent -in content.csv -dsn <dsn>")
		os.Exit(2)
	}
	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}

	rows, err := csvload.ReadContentCSV(*inPath)
	must(err, "read content csv")

	ctx := context.Background()
	conn, err := chclient.Connect(ctx, *dsn)
	must(err, "connect")
	defer conn.Close()

	// content_metadata is small and keyed by content_id; truncate+reload is fine.
	must(conn.Exec(ctx, "TRUNCATE TABLE IF EXISTS "+cfg.Database+".content_metadata"), "truncate")
	must(chclient.InsertContent(ctx, conn, cfg.Database+".content_metadata", rows), "insert content")
	must(conn.Exec(ctx, "SYSTEM RELOAD DICTIONARY "+cfg.Database+".content_dict"), "reload dict")
	fmt.Printf("loaded %d content rows into %s.content_metadata + reloaded dict\n", len(rows), cfg.Database)
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}
