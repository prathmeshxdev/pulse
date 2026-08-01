// Package csvload reads the Sony LIV raw-events CSV into typed RawEvents.
// Shared by cmd/build_segments and cmd/loadraw so the parsing is identical.
package csvload

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/prathmeshxdev/pulse/internal/models"
)

// ReadCSV parses the raw-events CSV (CSVWithNames) into RawEvents.
func ReadCSV(path string) ([]models.RawEvent, error) {
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
	for _, k := range []string{"video_session_id", "user_id", "content_id", "event_type", "event", "event_timestamp", "platform", "country"} {
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
		ts, err := ParseTS(get("event_timestamp"))
		if err != nil {
			return nil, fmt.Errorf("timestamp: %w", err)
		}
		cid, _ := strconv.ParseUint(get("content_id"), 10, 64)
		sse := ts
		if v := get("session_start_epoch"); v != "" {
			if t, err := ParseTS(v); err == nil {
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

// ParseTS accepts epoch-ms (training CSV), epoch-s, or common RFC/SQL formats.
func ParseTS(s string) (time.Time, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1_000_000_000_000 {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", s)
}
