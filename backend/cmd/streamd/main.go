// streamd is the real-time streaming daemon: it replays raw events in
// event-time order (simulating a live feed — see -speed), applies each event
// to that session's Redis-backed Accumulator (internal/livestate), and writes
// finalized segments + their delta pairs to ClickHouse the instant a segment
// closes. It never rebuilds and never rescans history — cost per event is
// O(1) Redis round-trip, cost per finalized segment is one small ClickHouse
// insert.
//
// This is the design validated in internal/segments (TestStreamingMatchesBatch,
// TestSnapshotRoundTrip) and internal/livestate (TestApplyEvent_*), and
// end-to-end via cmd/validateredis against both synthetic and real
// production-shaped data: streaming through Redis produces byte-identical
// segments to the batch builder, so running this instead of (or alongside)
// the batch pipeline does not change answers — it changes freshness from
// "next batch run" to "sub-second".
//
// Late events (session already closed and evicted, i.e. its key's fixed —
// non-refreshing — TTL, default 72h = MAX_SEGMENT_SPAN_HOURS, safely above
// the measured 43.64h max session span, has expired) are NOT silently
// dropped: streamd logs them and leaves reconciliation to `cmd/reconcile`,
// which rebuilds from the durable raw_events log. This is the documented
// reconcile-or-drop boundary.
//
// A background Sweep() loop runs alongside replay (see -sweep-every) so the
// live active-count stays wall-clock accurate: the active set is otherwise
// only touched on writes, so a session that goes silent past the heartbeat
// grace without a closing event would linger as falsely-active until its own
// next event happens to trigger the gap check (caught by cmd/validateredis).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/csvload"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/livestate"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

func main() {
	inPath := flag.String("in", "", "raw events CSV to replay as a live stream")
	dsn := flag.String("dsn", os.Getenv("CLICKHOUSE_DSN"), "ClickHouse DSN — finalized segments/deltas are written here")
	redisAddr := flag.String("redis", defaultRedisAddr(), "Redis address for live session state (host:port)")
	redisPassword := flag.String("redis-password", os.Getenv("REDIS_PASSWORD"), "Redis password (or set REDIS_PASSWORD)")
	configPath := flag.String("config", "", "path to config.env")
	speed := flag.Float64("speed", 0, "replay speed multiplier of real time (0 = as fast as possible, no sleeping)")
	ttlHours := flag.Int("ttl-hours", 72, "fixed (non-refreshing) Redis state TTL / max accepted lateness, in hours — default = MAX_SEGMENT_SPAN_HOURS")
	statusEvery := flag.Duration("status-every", 5*time.Second, "how often to print active-count status")
	sweepEvery := flag.Duration("sweep-every", 10*time.Second, "how often (in the REPLAYED event timeline, not wall-clock) to evict silent sessions from the active set — required for an accurate active-count read; see livestate.Store.Sweep")
	flag.Parse()

	if *inPath == "" || *dsn == "" {
		fmt.Fprintln(os.Stderr, "usage: streamd -in raw.csv -dsn <dsn> [-redis host:port] [-speed 60]")
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
	// Canonical (timestamp, event_type, event) order — same-timestamp ties MUST
	// break identically to the batch builder (segments.SortEvents), or a
	// streamed session can diverge from its batch-built segments. See
	// segments.EventLess doc for the real-data bug this guards against.
	segments.SortEvents(events)
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "no events")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := chclient.Connect(ctx, *dsn)
	must(err, "connect clickhouse")
	defer conn.Close()

	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr, Password: *redisPassword})
	must(rdb.Ping(ctx).Err(), "connect redis")
	defer rdb.Close()

	store := livestate.New(rdb, cfg, time.Duration(*ttlHours)*time.Hour)
	version := uint64(time.Now().Unix())

	fmt.Printf("streamd: replaying %d events from %s (speed=%v, ttl=%dh)\n", len(events), *inPath, *speed, *ttlHours)

	var (
		processed, finalized, evicted int
		lastStatus                    time.Time
		lastSweep                     = events[0].EventTimestamp
		firstEventTS                  = events[0].EventTimestamp
		replayStart                   = time.Now()
	)

	for _, e := range events {
		select {
		case <-ctx.Done():
			fmt.Println("\nstreamd: shutting down (signal received)")
			printSummary(processed, finalized, evicted)
			return
		default:
		}

		if *speed > 0 {
			// Pace replay to simulate real event arrival cadence.
			wantElapsed := e.EventTimestamp.Sub(firstEventTS)
			target := replayStart.Add(time.Duration(float64(wantElapsed) / *speed))
			if d := time.Until(target); d > 0 {
				time.Sleep(d)
			}
		}

		closedSegs, openSeg, err := store.ApplyEventAndSnapshot(ctx, e, version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "streamd: apply event for session %s: %v\n", e.VideoSessionID, err)
			continue
		}
		processed++

		if len(closedSegs) > 0 {
			if err := writeFinalized(ctx, conn, cfg.Database, closedSegs); err != nil {
				fmt.Fprintf(os.Stderr, "streamd: write finalized segments: %v\n", err)
			}
			finalized += len(closedSegs)
		}
		// Write the still-open segment on every event, not just on close, so
		// "active now" queries against session_active_segments FINAL see this
		// session immediately instead of waiting for it to end. Segment-table
		// write only (no delta emission) — the close, when it happens, is what
		// still drives minute_deltas, so this can't double-count.
		if openSeg != nil {
			if err := chclient.InsertSegments(ctx, conn, cfg.Database+".session_active_segments", []models.Segment{*openSeg}); err != nil {
				fmt.Fprintf(os.Stderr, "streamd: write open segment snapshot: %v\n", err)
			}
		}

		// Sweep on the REPLAYED (event-time) clock, not wall-clock: at
		// -speed=0 (fastest possible) real time barely advances even though
		// the simulated timeline covers the whole dataset, so a wall-clock
		// ticker would never fire during replay and the active set would run
		// stale exactly the way cmd/validateredis caught. In a true live
		// deployment (events arriving close to real time), this is
		// equivalent to a periodic wall-clock sweep.
		if e.EventTimestamp.Sub(lastSweep) >= *sweepEvery {
			n, err := store.Sweep(ctx, e.EventTimestamp)
			if err != nil {
				fmt.Fprintf(os.Stderr, "streamd: sweep: %v\n", err)
			} else {
				evicted += n
			}
			lastSweep = e.EventTimestamp
		}

		if time.Since(lastStatus) >= *statusEvery {
			n, _ := store.ActiveCount(ctx)
			fmt.Printf("  [t=%s] processed=%d finalized_segments=%d active_now=%d\n",
				e.EventTimestamp.UTC().Format(time.RFC3339), processed, finalized, n)
			lastStatus = time.Now()
		}
	}

	// Final sweep so the closing summary/active-count reflects fully-settled state.
	if n, err := store.Sweep(ctx, events[len(events)-1].EventTimestamp); err == nil {
		evicted += n
	}

	fmt.Println("streamd: replay complete")
	printSummary(processed, finalized, evicted)
}

// writeFinalized inserts newly-closed segments plus their any-overlap delta
// pairs directly. No staging/REPLACE PARTITION here — those are batch-load
// idempotency tools for rebuilding a whole partition; a streaming finalize is
// a one-time append of a segment that has never been written before (its
// segment_id is derived from session_id+segment_start, so it can't collide
// with anything the batch pipeline already wrote for the same boundaries), so
// a plain INSERT is correct and far cheaper.
func writeFinalized(ctx context.Context, conn driver.Conn, database string, segs []models.Segment) error {
	if err := chclient.InsertSegments(ctx, conn, database+".session_active_segments", segs); err != nil {
		return fmt.Errorf("insert segments: %w", err)
	}
	rows := deltas.EmitAll(segs)
	if err := chclient.InsertDeltas(ctx, conn, database+".minute_deltas", rows); err != nil {
		return fmt.Errorf("insert deltas: %w", err)
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// defaultRedisAddr accepts either REDIS_ADDR or the REDIS_HOST/REDIS_PORT pair
// (the shape managed providers like Redis Cloud hand out with REDIS_PASSWORD).
func defaultRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	if host := os.Getenv("REDIS_HOST"); host != "" {
		return host + ":" + envOr("REDIS_PORT", "6379")
	}
	return "localhost:6379"
}

func printSummary(processed, finalized, evicted int) {
	fmt.Printf("streamd: processed=%d events, finalized=%d segments, swept=%d stale-active evictions\n", processed, finalized, evicted)
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", ctx, err)
		os.Exit(1)
	}
}
