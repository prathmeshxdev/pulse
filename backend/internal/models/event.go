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
}

// Content is one row from the content metadata CSV → content_metadata.
type Content struct {
	ContentID uint64
	Title     string
	VideoType string
	Category  string
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
	SignalPause      Signal = "pause"
	SignalResume     Signal = "resume"
	SignalKeepalive  Signal = "keepalive"
	SignalIgnore     Signal = "ignore"
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
}

// MinuteDelta is one narrow sweep-line edge.
type MinuteDelta struct {
	Minute    time.Time
	SegmentID uint64
	Delta     int64
}

// Close reasons (ACTIVE_INTERVAL_LOGIC Step 2).
const (
	CloseReasonPause       = "pause"
	CloseReasonBackground  = "background"
	CloseReasonHeartbeat   = "heartbeat_gap"
	CloseReasonSessionEnd  = "session_end"
	CloseReasonError       = "error"
	CloseReasonWatermark   = "open_at_watermark"
)
