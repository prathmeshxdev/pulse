// Package schema defines the fixed typed columns on raw_events and which CSV /
// Kafka fields map to them. Any other incoming field is stored in properties
// (JSON) so new dimensions need no DDL migration.
package schema

// RawEventKnownColumns are CSV/Kafka headers mapped to typed raw_events columns.
// Keep in sync with clickhouse/migrations/002_raw_events.sql (excluding ingest metadata).
var RawEventKnownColumns = map[string]struct{}{
	"video_session_id":    {},
	"user_id":             {},
	"content_id":          {},
	"event_type":          {},
	"event":               {},
	"event_timestamp":     {},
	"platform":            {},
	"app_version":         {},
	"country":             {},
	"audio_language":      {},
	"subtitle_language":   {},
	"player_version":      {},
	"session_start_epoch": {},
}
