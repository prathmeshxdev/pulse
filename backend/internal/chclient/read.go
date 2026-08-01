package chclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

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
		subtitle_language, player_version, session_start_epoch
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
		if err := rows.Scan(&e.VideoSessionID, &e.UserID, &e.ContentID, &e.EventType, &e.Event,
			&e.EventTimestamp, &e.Platform, &e.AppVersion, &e.Country, &e.AudioLanguage,
			&e.SubtitleLanguage, &e.PlayerVersion, &e.SessionStartEpoch); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
