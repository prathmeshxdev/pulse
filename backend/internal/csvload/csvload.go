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
	"strings"
	"time"

	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/schema"
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
		props := extraProperties(idx, rec, get)
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
			Properties:        props,
		})
	}
	return out, nil
}

// extraProperties collects CSV columns that are not part of the typed schema.
func extraProperties(idx map[string]int, rec []string, get func(string) string) map[string]interface{} {
	var props map[string]interface{}
	for col := range idx {
		if _, known := schema.RawEventKnownColumns[col]; known {
			continue
		}
		v := get(col)
		if v == "" {
			continue
		}
		if props == nil {
			props = make(map[string]interface{})
		}
		props[col] = parsePropertyValue(v)
	}
	return props
}

func parsePropertyValue(s string) interface{} {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}

// ReadContentCSV parses the content metadata CSV
// (content_id,title,video_type,category[,show_name]).
func ReadContentCSV(path string) ([]models.Content, error) {
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
	for _, k := range []string{"content_id", "title", "video_type", "category"} {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("missing column %s", k)
		}
	}
	get := func(rec []string, k string) string {
		i, ok := idx[k]
		if !ok || i >= len(rec) {
			return ""
		}
		return rec[i]
	}
	var out []models.Content
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		cid, _ := strconv.ParseUint(get(rec, "content_id"), 10, 64)
		out = append(out, models.Content{
			ContentID: cid,
			Title:     get(rec, "title"),
			VideoType: get(rec, "video_type"),
			Category:  get(rec, "category"),
			ShowName:  get(rec, "show_name"),
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
