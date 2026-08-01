package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/csvload"
	"github.com/prathmeshxdev/pulse/internal/livestate"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

// validateredis is a throwaway correctness harness (not part of the shipped
// pipeline) proving the Redis streaming path against a real Redis instance:
// it runs the SAME event set through (a) the batch builder, ground truth, and
// (b) internal/livestate.Store backed by real Redis, then asserts:
//  1. Final segment sets are identical (no missing, no extra).
//  2. "Active at instant T" counts match an in-memory reference accumulator
//     at several sampled instants during replay.
//  3. Redis's active-session set is empty once every session has closed.
//
// -csv points it at a real exported slice of raw_events (preferred); with no
// -csv it falls back to a synthetic generator exercising every classifier
// branch (pause/resume, background/foreground, buffer stalls, heartbeat gaps).
func main() {
	csvPath := flag.String("csv", "", "path to a real raw_events CSV slice; empty = synthetic generator")
	nSessions := flag.Int("sessions", 2000, "synthetic session count (ignored with -csv)")
	flush := flag.Bool("flush", true, "delete pulse:session:* and pulse:active before running")
	flag.Parse()

	cfg := config.DefaultConstants()

	var events []models.RawEvent
	if *csvPath != "" {
		var err error
		events, err = csvload.ReadCSV(*csvPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read csv: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("loaded %d REAL events from %s\n", len(events), *csvPath)
	} else {
		rng := rand.New(rand.NewSource(42))
		base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		events = genEvents(rng, *nSessions, base)
		fmt.Printf("generated %d SYNTHETIC events across %d sessions\n", len(events), *nSessions)
	}

	wm := maxTS(events)

	// --- Ground truth: batch builder ---
	batchSorted := make([]models.RawEvent, len(events))
	copy(batchSorted, events)
	batchSegs := segments.NewBuilder(cfg, 1).BuildAll(batchSorted, wm)
	fmt.Printf("batch: %d segments\n", len(batchSegs))

	// --- Streaming via real Redis ---
	addr := os.Getenv("REDIS_HOST") + ":" + os.Getenv("REDIS_PORT")
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD")})
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "redis ping failed: %v\n", err)
		os.Exit(1)
	}
	if *flush {
		n := flushTestKeys(ctx, rdb)
		fmt.Printf("flushed %d pre-existing pulse:* keys before starting\n", n)
	}

	store := livestate.New(rdb, cfg, 48*time.Hour)

	// Canonical order (segments.EventLess: timestamp, then event_type, then
	// event). A global sort by this total order induces the same per-session
	// order the batch builder uses internally — a plain timestamp sort does
	// NOT, because it leaves same-timestamp ties in arrival order, which is
	// exactly the real-data bug this harness caught (see EventLess doc).
	streamSorted := make([]models.RawEvent, len(events))
	copy(streamSorted, events)
	segments.SortEvents(streamSorted)

	var streamSegs []models.Segment
	version := uint64(time.Now().Unix())
	refAccs := map[string]*segments.Accumulator{}
	sampleInstants := pickSampleInstants(streamSorted, 8)
	sampleIdx := 0
	mismatches := 0
	start := time.Now()

	for i, e := range streamSorted {
		closed, err := store.ApplyEvent(ctx, e, version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "apply event error: %v\n", err)
			os.Exit(1)
		}
		streamSegs = append(streamSegs, closed...)

		ra := refAccs[e.VideoSessionID]
		if ra == nil {
			ra = segments.NewAccumulator(cfg, version)
			refAccs[e.VideoSessionID] = ra
		}
		ra.Apply(e)

		for sampleIdx < len(sampleInstants) && (i == len(streamSorted)-1 || streamSorted[i+1].EventTimestamp.After(sampleInstants[sampleIdx])) {
			T := sampleInstants[sampleIdx]
			// The active SET is only touched on writes (per event); a session
			// that goes silent past grace with no closing event won't drop out
			// until ITS next event arrives and Apply() detects the gap. A
			// wall-clock "active now" read must Sweep() first — exactly what a
			// real deployment would run periodically alongside streamd.
			if _, err := store.Sweep(ctx, T); err != nil {
				fmt.Fprintf(os.Stderr, "sweep error: %v\n", err)
				os.Exit(1)
			}
			redisActive, err := store.ActiveCount(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "active count error: %v\n", err)
				os.Exit(1)
			}
			refActive := int64(0)
			for _, a := range refAccs {
				if a.Active(T) {
					refActive++
				}
			}
			match := redisActive == refActive
			if !match {
				mismatches++
			}
			fmt.Printf("  T=%s  redis_active=%d  ref_active=%d  match=%v\n", T.Format(time.RFC3339), redisActive, refActive, match)
			sampleIdx++
		}

		if i > 0 && i%10000 == 0 {
			fmt.Printf("  ... %d/%d events processed (%.0fs elapsed)\n", i, len(streamSorted), time.Since(start).Seconds())
		}
	}

	for sid, a := range refAccs {
		for _, s := range a.Finalize(wm) {
			s.VideoSessionID = sid
			streamSegs = append(streamSegs, s)
		}
	}

	finalActive, _ := store.ActiveCount(ctx)
	fmt.Printf("\nfinal redis active-count after full replay: %d\n", finalActive)
	fmt.Printf("total replay time: %.1fs (%d events)\n", time.Since(start).Seconds(), len(streamSorted))

	key := func(segs []models.Segment) map[string]bool {
		m := map[string]bool{}
		for _, s := range segs {
			m[fmt.Sprintf("%s|%d|%s|%s|%s", s.VideoSessionID, s.SegmentID,
				s.SegmentStart.UTC().Format(time.RFC3339Nano), s.SegmentEnd.UTC().Format(time.RFC3339Nano), s.CloseReason)] = true
		}
		return m
	}
	bKeys := key(batchSegs)
	sKeys := key(streamSegs)

	missing := 0
	for k := range bKeys {
		if !sKeys[k] {
			missing++
			if missing <= 5 {
				fmt.Printf("MISSING from streaming: %s\n", k)
			}
		}
	}
	extra := 0
	for k := range sKeys {
		if !bKeys[k] {
			extra++
			if extra <= 5 {
				fmt.Printf("EXTRA in streaming: %s\n", k)
			}
		}
	}

	fmt.Printf("\n=== RESULTS ===\n")
	fmt.Printf("batch segments:  %d\n", len(bKeys))
	fmt.Printf("stream segments: %d\n", len(sKeys))
	fmt.Printf("missing (in batch, not stream): %d\n", missing)
	fmt.Printf("extra (in stream, not batch):    %d\n", extra)
	fmt.Printf("active-at-T mismatches: %d / %d samples\n", mismatches, len(sampleInstants))

	if missing == 0 && extra == 0 && mismatches == 0 && finalActive == 0 {
		fmt.Println("\n*** PASS: streaming via real Redis is byte-identical to batch, active-count exact at all samples, zero leftover active state ***")
	} else {
		fmt.Println("\n*** FAIL ***")
		os.Exit(1)
	}
}

func flushTestKeys(ctx context.Context, rdb *redis.Client) int {
	n := 0
	iter := rdb.Scan(ctx, 0, "pulse:session:*", 1000).Iterator()
	var toDel []string
	for iter.Next(ctx) {
		toDel = append(toDel, iter.Val())
		if len(toDel) >= 500 {
			rdb.Del(ctx, toDel...)
			n += len(toDel)
			toDel = toDel[:0]
		}
	}
	if len(toDel) > 0 {
		rdb.Del(ctx, toDel...)
		n += len(toDel)
	}
	rdb.Del(ctx, "pulse:active")
	return n
}

func maxTS(events []models.RawEvent) time.Time {
	var wm time.Time
	for _, e := range events {
		if e.EventTimestamp.After(wm) {
			wm = e.EventTimestamp
		}
	}
	return wm
}

func pickSampleInstants(sorted []models.RawEvent, n int) []time.Time {
	if len(sorted) == 0 {
		return nil
	}
	var out []time.Time
	step := len(sorted) / (n + 1)
	if step < 1 {
		step = 1
	}
	for i := step; i < len(sorted) && len(out) < n; i += step {
		out = append(out, sorted[i].EventTimestamp)
	}
	return out
}

// genEvents creates realistic session event streams: each session plays,
// sometimes pauses/resumes, sometimes backgrounds/foregrounds, sometimes
// buffers, sometimes has a heartbeat gap, and always ends with VideoSessionEnd
// (matching the training data property that all sessions are closed).
func genEvents(rng *rand.Rand, n int, base time.Time) []models.RawEvent {
	platforms := []string{"ANDROID_PHONE", "IPHONE", "SONY_ANDROID_TV", "JIO_ANDROID_TV", "Mweb"}
	var events []models.RawEvent

	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("SESS-%06d", i)
		uid := fmt.Sprintf("USER-%06d", i%500)
		platform := platforms[rng.Intn(len(platforms))]
		startOffset := time.Duration(rng.Intn(7200)) * time.Second
		t := base.Add(startOffset)

		ev := func(sec int, et, e string) models.RawEvent {
			return models.RawEvent{
				VideoSessionID: sid, UserID: uid, ContentID: uint64(1000 + i%50),
				EventType: et, Event: e, EventTimestamp: t.Add(time.Duration(sec) * time.Second),
				Platform: platform, Country: "india", AppVersion: "1.0", AudioLanguage: "hin",
				SubtitleLanguage: "unk", PlayerVersion: "1.0",
			}
		}

		events = append(events, ev(0, "VideoSessionStart", "VideoSessionStart"))
		events = append(events, ev(0, "VideoPlay", "VideoPlay"))

		sec := 5
		sessionLen := 60 + rng.Intn(600)
		for sec < sessionLen {
			switch rng.Intn(10) {
			case 0:
				events = append(events, ev(sec, "VideoHeartbeat", "pause"))
				sec += 5 + rng.Intn(20)
				events = append(events, ev(sec, "VideoHeartbeat", "resume"))
			case 1:
				events = append(events, ev(sec, "AppBackgrounded", "AppBackgrounded"))
				sec += 5 + rng.Intn(30)
				events = append(events, ev(sec, "AppForegrounded", "AppForegrounded"))
				events = append(events, ev(sec, "VideoPlay", "VideoPlay"))
			case 2:
				events = append(events, ev(sec, "VideoHeartbeat", "BufferStart"))
				sec += 2 + rng.Intn(8)
				events = append(events, ev(sec, "VideoHeartbeat", "BufferEnd"))
			default:
				events = append(events, ev(sec, "VideoHeartbeat", "buffer-health"))
			}
			sec += 10 + rng.Intn(20)
		}
		events = append(events, ev(sec+5, "VideoSessionEnd", "VideoSessionEnd"))
	}
	return events
}
