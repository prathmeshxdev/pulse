package models

import "time"

// RawEvent is one row from raw_events / the training CSV.
type RawEvent struct {
	VideoSessionID    string
	UserID            string
	ContentID         uint64
	EventType         string
	Event             string
	EventTimestamp    time.Time
	Platform          string
	AppVersion        string
	Country           string
	AudioLanguage     string
	SubtitleLanguage  string
	PlayerVersion     string
	SessionStartEpoch time.Time
	// Properties holds any CSV/Kafka columns not in the typed schema (dynamic extensibility).
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// Content is one row from the content metadata CSV → content_metadata.
type Content struct {
	ContentID uint64
	Title     string
	VideoType string
	Category  string
	ShowName  string
}

// Signal is the classified event semantics (FINAL_PLAN §1.3).
type Signal string

const (
	SignalOpen       Signal = "open"
	SignalClose      Signal = "close"
	SignalPlay       Signal = "play"
	SignalBackground Signal = "background"
	SignalForeground Signal = "foreground"
	SignalError      Signal = "error"
	SignalPause       Signal = "pause"
	SignalResume      Signal = "resume"
	SignalKeepalive   Signal = "keepalive"
	SignalBufferStart Signal = "buffer_start"
	SignalBufferEnd   Signal = "buffer_end"
	SignalIgnore      Signal = "ignore"
)

// Segment is one contiguous foreground-active interval.
type Segment struct {
	SegmentID        uint64
	VideoSessionID   string
	UserID           string
	ContentID        uint64
	Platform         string
	Country          string
	AppVersion       string
	AudioLanguage    string
	SubtitleLanguage string
	PlayerVersion    string
	VideoType        string
	Category         string
	SegmentStart     time.Time
	SegmentEnd       time.Time // exclusive
	IsFinal          uint8
	CloseReason      string
	Version          uint64
	// Properties snapshots dynamic dimensions from the opening event (R10).
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// MinuteDelta is one narrow sweep-line edge (session grain).
type MinuteDelta struct {
	Minute    time.Time
	SegmentID uint64
	Delta     int64
}

// UserSegment is one merged foreground-active interval per user (session-independent).
type UserSegment struct {
	UserSegmentID    uint64
	UserID           string
	ContentID        uint64
	Platform         string
	Country          string
	AppVersion       string
	AudioLanguage    string
	SubtitleLanguage string
	PlayerVersion    string
	VideoType        string
	Category         string
	SegmentStart     time.Time
	SegmentEnd       time.Time // exclusive
	CloseReason      string    // empty while any contributing session is still open
	Version          uint64
	Properties       map[string]interface{} `json:"properties,omitempty"`
}

// UserMinuteDelta is one sweep-line edge at user grain.
type UserMinuteDelta struct {
	Minute          time.Time
	UserSegmentID   uint64
	Delta           int64
}

// WideDelta is one sweep-line edge for the optional denormalized rollup
// (concurrency_minute_serving): dimensions ride on the row instead of a segment_id.
type WideDelta struct {
	Minute           time.Time
	Platform         string
	Country          string
	ContentID        uint64
	AppVersion       string
	AudioLanguage    string
	SubtitleLanguage string
	PlayerVersion    string
	Delta            int64
}

// Close reasons (ACTIVE_INTERVAL_LOGIC Step 2).
const (
	CloseReasonPause       = "pause"
	CloseReasonBackground  = "background"
	CloseReasonHeartbeat   = "heartbeat_gap"
	CloseReasonSessionEnd  = "session_end"
	CloseReasonError       = "error"
	CloseReasonBuffer      = "buffer"
	CloseReasonWatermark   = "open_at_watermark"
)
