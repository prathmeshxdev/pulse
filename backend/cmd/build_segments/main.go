package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

// build_segments reads raw events (CSV or JSONL) and emits segments + deltas as JSONL.
// This is the independent Go reference path (FINAL_PLAN Phase 1 / ACTIVE_INTERVAL_LOGIC §3c).
func main() {
	inPath := flag.String("in", "", "input CSV path (Sony LIV raw events)")
	outSeg := flag.String("segments", "segments.jsonl", "output segments JSONL")
	outDelta := flag.String("deltas", "deltas.jsonl", "output deltas JSONL")
	configPath := flag.String("config", "", "path to config.env")
	version := flag.Uint64("version", 1, "pipeline run version")
	watermarkStr := flag.String("watermark", "", "optional RFC3339 watermark; default = max event ts")
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

	events, err := readCSV(*inPath)
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

	if err := writeJSONL(*outSeg, segs); err != nil {
		fmt.Fprintf(os.Stderr, "segments: %v\n", err)
		os.Exit(1)
	}
	if err := writeJSONL(*outDelta, drows); err != nil {
		fmt.Fprintf(os.Stderr, "deltas: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("events=%d segments=%d deltas=%d watermark=%s\n",
		len(events), len(segs), len(drows), wm.UTC().Format(time.RFC3339Nano))
}

func readCSV(path string) ([]models.RawEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReader(f))
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	required := []string{
		"video_session_id", "user_id", "content_id", "event_type", "event",
		"event_timestamp", "platform", "country",
	}
	for _, k := range required {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("missing column %s", k)
		}
	}

	var out []models.RawEvent
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		get := func(k string) string {
			i, ok := idx[k]
			if !ok || i >= len(rec) {
				return ""
			}
			return rec[i]
		}
		ts, err := parseTS(get("event_timestamp"))
		if err != nil {
			return nil, fmt.Errorf("timestamp: %w", err)
		}
		cid, _ := strconv.ParseUint(get("content_id"), 10, 64)
		sse := ts
		if v := get("session_start_epoch"); v != "" {
			if t, err := parseTS(v); err == nil {
				sse = t
			}
		}
		out = append(out, models.RawEvent{
			VideoSessionID:    get("video_session_id"),
			UserID:            get("user_id"),
			ContentID:         cid,
			EventType:         get("event_type"),
			Event:             get("event"),
			EventTimestamp:    ts,
			Platform:          get("platform"),
			AppVersion:        get("app_version"),
			Country:           get("country"),
			AudioLanguage:     get("audio_language"),
			SubtitleLanguage:  get("subtitle_language"),
			PlayerVersion:     get("player_version"),
			SessionStartEpoch: sse,
		})
	}
	return out, nil
}

func parseTS(s string) (time.Time, error) {
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		// epoch ms (training CSV)
		if ms > 1_000_000_000_000 {
			return time.UnixMilli(ms).UTC(), nil
		}
		return time.Unix(ms, 0).UTC(), nil
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", s)
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
