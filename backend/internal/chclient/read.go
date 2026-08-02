package chclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/prathmeshxdev/pulse/internal/filters"
	"github.com/prathmeshxdev/pulse/internal/models"
)

// FetchSessionEvents reads all raw_events for the given sessions, ordered for
// deterministic replay. Used by reconcile to recompute a session end-to-end.
func FetchSessionEvents(ctx context.Context, conn driver.Conn, db string, sessionIDs []string) ([]models.RawEvent, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(sessionIDs))
	for i, s := range sessionIDs {
		quoted[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	sql := fmt.Sprintf(`SELECT video_session_id, user_id, content_id, event_type, event,
		event_timestamp, platform, app_version, country, audio_language,
		subtitle_language, player_version, session_start_epoch, properties
		FROM %s.raw_events WHERE video_session_id IN (%s)
		ORDER BY video_session_id, event_timestamp, event_type, event`,
		db, strings.Join(quoted, ", "))
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.RawEvent
	for rows.Next() {
		var e models.RawEvent
		var props clickhouse.JSON
		if err := rows.Scan(&e.VideoSessionID, &e.UserID, &e.ContentID, &e.EventType, &e.Event,
			&e.EventTimestamp, &e.Platform, &e.AppVersion, &e.Country, &e.AudioLanguage,
			&e.SubtitleLanguage, &e.PlayerVersion, &e.SessionStartEpoch, &props); err != nil {
			return nil, err
		}
		e.Properties = PropertiesFromJSON(&props)
		out = append(out, e)
	}
	return out, rows.Err()
}

// FetchAllSegments reads all session_active_segments (FINAL) for user-island rebuild.
func FetchAllSegments(ctx context.Context, conn driver.Conn, db string) ([]models.Segment, error) {
	sql := fmt.Sprintf(`SELECT segment_id, video_session_id, user_id, content_id, platform, country,
		app_version, audio_language, subtitle_language, player_version,
		video_type, category,
		segment_start, segment_end, is_final, close_reason, version, properties
		FROM %s.session_active_segments FINAL`, db)
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Segment
	for rows.Next() {
		var s models.Segment
		var props clickhouse.JSON
		if err := rows.Scan(&s.SegmentID, &s.VideoSessionID, &s.UserID, &s.ContentID,
			&s.Platform, &s.Country, &s.AppVersion, &s.AudioLanguage, &s.SubtitleLanguage,
			&s.PlayerVersion, &s.VideoType, &s.Category,
			&s.SegmentStart, &s.SegmentEnd, &s.IsFinal, &s.CloseReason, &s.Version, &props); err != nil {
			return nil, err
		}
		s.Properties = PropertiesFromJSON(&props)
		out = append(out, s)
	}
	return out, rows.Err()
}

// TableExists reports whether database.table exists (used to make the optional
// rollup population a no-op when the table isn't migrated).
func TableExists(ctx context.Context, conn driver.Conn, database, table string) bool {
	rows, err := QueryMaps(ctx, conn, fmt.Sprintf(
		"SELECT count() AS n FROM system.tables WHERE database='%s' AND name='%s'", database, table))
	if err != nil || len(rows) == 0 {
		return false
	}
	switch v := rows[0]["n"].(type) {
	case uint64:
		return v > 0
	case int64:
		return v > 0
	}
	return false
}

// SegmentIDsForSessions returns segment_ids currently attributed to the given
// sessions — the set whose published delta edges a reconcile must cancel.
func SegmentIDsForSessions(ctx context.Context, conn driver.Conn, db string, sessionIDs []string) ([]uint64, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(sessionIDs))
	for i, s := range sessionIDs {
		quoted[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	sql := fmt.Sprintf(`SELECT DISTINCT segment_id FROM %s.session_active_segments FINAL
		WHERE video_session_id IN (%s)`, db, strings.Join(quoted, ", "))
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PublishedEdges reads the currently-published net delta per (minute, segment_id)
// for the given segments — the merge-independent source of truth for what to
// cancel (FINAL_PLAN §8.2). Reading it back (rather than caching) is what makes
// a repeated reconcile a no-op.
func PublishedEdges(ctx context.Context, conn driver.Conn, db string, segmentIDs []uint64) ([]models.MinuteDelta, error) {
	if len(segmentIDs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(segmentIDs))
	for i, s := range segmentIDs {
		ids[i] = fmt.Sprintf("%d", s)
	}
	sql := fmt.Sprintf(`SELECT minute, segment_id, sum(delta) AS d
		FROM %s.minute_deltas WHERE segment_id IN (%s)
		GROUP BY minute, segment_id HAVING d <> 0`, db, strings.Join(ids, ", "))
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.MinuteDelta
	for rows.Next() {
		var d models.MinuteDelta
		if err := rows.Scan(&d.Minute, &d.SegmentID, &d.Delta); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FetchPropertyKeyTypes reads the daily append catalog (path → types map).
// Merges all appended snapshots; returns nil when not migrated yet.
func FetchPropertyKeyTypes(ctx context.Context, conn driver.Conn, db string) (filters.PropertyTypes, error) {
	if TableExists(ctx, conn, db, "properties_key_mappings") {
		sql := fmt.Sprintf(`SELECT
			k AS key,
			any(arrayElement(v, 1)) AS ch_type
		FROM %s.properties_key_mappings
		ARRAY JOIN mapKeys(paths) AS k, paths[k] AS v
		GROUP BY k`, db)
		return scanPropertyTypes(ctx, conn, sql)
	}
	// Legacy flat table (pre-daily-append migration).
	if TableExists(ctx, conn, db, "properties_key_types") {
		sql := fmt.Sprintf(`SELECT key, any(ch_type) AS ch_type
			FROM %s.properties_key_types FINAL
			WHERE source IN ('session_active_segments', 'raw_events')
			GROUP BY key`, db)
		return scanPropertyTypes(ctx, conn, sql)
	}
	return nil, nil
}

func scanPropertyTypes(ctx context.Context, conn driver.Conn, sql string) (filters.PropertyTypes, error) {
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(filters.PropertyTypes)
	for rows.Next() {
		var key, chType string
		if err := rows.Scan(&key, &chType); err != nil {
			return nil, err
		}
		out[key] = chType
	}
	return out, rows.Err()
}
