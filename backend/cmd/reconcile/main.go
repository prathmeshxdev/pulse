// reconcile applies the incremental correction path for late-arriving events
// or open sessions, WITHOUT a full rebuild (FINAL_PLAN §8.2).
//
// For the affected sessions it:
//  1. re-reads all of their raw_events from ClickHouse and recomputes segments,
//  2. reads the currently *published* delta edges back out of minute_deltas,
//  3. cancels exactly those edges and writes the new ones,
//  4. replaces the segment rows with a higher version.
//
// Because the edge to cancel is read from minute_deltas (not cached), running
// reconcile twice is a no-op — the second run cancels the new edge and rewrites
// the same new edge. minute_deltas is a SummingMergeTree, so the cancel/rewrite
// pair collapses to the net on the next merge and is already correct via
// sum(delta) before it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN")
	sessionsCSV := flag.String("sessions", "", "comma-separated video_session_id list to reconcile")
	configPath := flag.String("config", "", "path to config.env")
	version := flag.Uint64("version", uint64(time.Now().Unix()), "rebuild version (must increase across runs)")
	watermarkStr := flag.String("watermark", "", "RFC3339 watermark; default = max(event_timestamp) of affected sessions")
	flag.Parse()

	if *dsn == "" || *sessionsCSV == "" {
		fmt.Fprintln(os.Stderr, "usage: reconcile -dsn <dsn> -sessions id1,id2   (or -sessions - to read from stdin)")
		os.Exit(2)
	}
	cfg := config.DefaultConstants()
	if *configPath != "" {
		if c, err := config.LoadConstantsFromEnvFile(*configPath); err == nil {
			cfg = c
		}
	}
	src := *sessionsCSV
	if src == "-" {
		b, err := io.ReadAll(os.Stdin)
		must(err, "read stdin")
		src = string(b)
	}
	sessions := splitTrim(src)

	ctx := context.Background()
	conn, err := chclient.Connect(ctx, *dsn)
	must(err, "connect")
	defer conn.Close()

	events, err := chclient.FetchSessionEvents(ctx, conn, cfg.Database, sessions)
	must(err, "fetch events")
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "no events for those sessions")
		os.Exit(1)
	}

	wm := time.Time{}
	for _, e := range events {
		if e.EventTimestamp.After(wm) {
			wm = e.EventTimestamp
		}
	}
	if *watermarkStr != "" {
		if t, err := time.Parse(time.RFC3339, *watermarkStr); err == nil {
			wm = t
		}
	}

	// 1. Recompute segments for the affected sessions.
	b := segments.NewBuilder(cfg, *version)
	newSegs := b.BuildAll(events, wm)
	newDeltas := deltas.EmitAll(newSegs)

	// 2. Set of edges to cancel: union of old (currently attributed) and new segment_ids.
	oldIDs, err := chclient.SegmentIDsForSessions(ctx, conn, cfg.Database, sessions)
	must(err, "old segment ids")
	idSet := map[uint64]struct{}{}
	for _, id := range oldIDs {
		idSet[id] = struct{}{}
	}
	for _, s := range newSegs {
		idSet[s.SegmentID] = struct{}{}
	}
	affected := make([]uint64, 0, len(idSet))
	for id := range idSet {
		affected = append(affected, id)
	}

	published, err := chclient.PublishedEdges(ctx, conn, cfg.Database, affected)
	must(err, "published edges")

	// 3. Corrections = cancel published edges + emit new deltas.
	corrections := make([]models.MinuteDelta, 0, len(published)+len(newDeltas))
	for _, p := range published {
		corrections = append(corrections, models.MinuteDelta{Minute: p.Minute, SegmentID: p.SegmentID, Delta: -p.Delta})
	}
	corrections = append(corrections, newDeltas...)

	must(chclient.InsertDeltas(ctx, conn, cfg.Database+".minute_deltas", corrections), "insert corrections")
	// 4. Replace segment rows (higher version → ReplacingMergeTree replaces).
	must(chclient.InsertSegments(ctx, conn, cfg.Database+".session_active_segments", newSegs), "insert segments")

	fmt.Printf("reconciled sessions=%d events=%d new_segments=%d cancelled_edges=%d new_deltas=%d version=%d\n",
		len(sessions), len(events), len(newSegs), len(published), len(newDeltas), *version)
}

// splitTrim accepts comma- or whitespace/newline-separated session ids.
func splitTrim(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}
