package chclient

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/prathmeshxdev/pulse/internal/models"
)

const insertChunkSize = 100_000

// InsertRawEvents batch-inserts into the given raw_events table (FQN, e.g.
// "sony_liv.raw_events" or a staging table). Sends in chunks so Cloud HTTP
// inserts stay under max_execution_time.
func InsertRawEvents(ctx context.Context, conn driver.Conn, table string, events []models.RawEvent) error {
	if len(events) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(`INSERT INTO %s
		(video_session_id, user_id, content_id, event_type, event, event_timestamp,
		 platform, app_version, country, audio_language, subtitle_language,
		 player_version, session_start_epoch, properties)`, table)
	for start := 0; start < len(events); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(events) {
			end = len(events)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, e := range events[start:end] {
			if err := batch.Append(
				e.VideoSessionID, e.UserID, e.ContentID, e.EventType, e.Event, e.EventTimestamp.UTC(),
				e.Platform, e.AppVersion, e.Country, e.AudioLanguage, e.SubtitleLanguage,
				e.PlayerVersion, e.SessionStartEpoch.UTC(), PropertiesColumn(e.Properties),
			); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("raw_events chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// InsertContent batch-inserts into content_metadata (FQN).
func InsertContent(ctx context.Context, conn driver.Conn, table string, rows []models.Content) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s (content_id, title, video_type, category, show_name)", table))
	if err != nil {
		return err
	}
	for _, c := range rows {
		if err := batch.Append(c.ContentID, c.Title, c.VideoType, c.Category, c.ShowName); err != nil {
			return err
		}
	}
	return batch.Send()
}

// InsertSegments batch-inserts into the given session_active_segments table
// (FQN). Columns match migration 005 (computed_at defaults).
func InsertSegments(ctx context.Context, conn driver.Conn, table string, segs []models.Segment) error {
	if len(segs) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(`INSERT INTO %s
		(segment_id, video_session_id, user_id, content_id, platform, country,
		 app_version, audio_language, subtitle_language, player_version,
		 segment_start, segment_end, is_final, close_reason, version, properties)`, table)
	for start := 0; start < len(segs); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(segs) {
			end = len(segs)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, s := range segs[start:end] {
			if err := batch.Append(
				s.SegmentID, s.VideoSessionID, s.UserID, s.ContentID, s.Platform, s.Country,
				s.AppVersion, s.AudioLanguage, s.SubtitleLanguage, s.PlayerVersion,
				s.SegmentStart.UTC(), s.SegmentEnd.UTC(), s.IsFinal, s.CloseReason, s.Version,
				PropertiesColumn(s.Properties),
			); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("segments chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// InsertDeltas batch-inserts into the given minute_deltas table (FQN).
func InsertDeltas(ctx context.Context, conn driver.Conn, table string, rows []models.MinuteDelta) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := fmt.Sprintf("INSERT INTO %s (minute, segment_id, delta)", table)
	for start := 0; start < len(rows); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, d := range rows[start:end] {
			if err := batch.Append(d.Minute.UTC(), d.SegmentID, d.Delta); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("deltas chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// InsertRollup batch-inserts wide deltas into concurrency_minute_serving (FQN).
func InsertRollup(ctx context.Context, conn driver.Conn, table string, rows []models.WideDelta) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(`INSERT INTO %s
		(minute, platform, country, content_id, app_version, audio_language,
		 subtitle_language, player_version, delta)`, table)
	for start := 0; start < len(rows); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, d := range rows[start:end] {
			if err := batch.Append(d.Minute.UTC(), d.Platform, d.Country, d.ContentID, d.AppVersion,
				d.AudioLanguage, d.SubtitleLanguage, d.PlayerVersion, d.Delta); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("rollup chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// InsertUserSegments batch-inserts into user_active_segments (FQN).
func InsertUserSegments(ctx context.Context, conn driver.Conn, table string, segs []models.UserSegment) error {
	if len(segs) == 0 {
		return nil
	}
	stmt := fmt.Sprintf(`INSERT INTO %s
		(user_segment_id, user_id, content_id, platform, country,
		 app_version, audio_language, subtitle_language, player_version,
		 video_type, category,
		 segment_start, segment_end, close_reason, version, properties)`, table)
	for start := 0; start < len(segs); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(segs) {
			end = len(segs)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, s := range segs[start:end] {
			if err := batch.Append(
				s.UserSegmentID, s.UserID, s.ContentID, s.Platform, s.Country,
				s.AppVersion, s.AudioLanguage, s.SubtitleLanguage, s.PlayerVersion,
				s.VideoType, s.Category,
				s.SegmentStart.UTC(), s.SegmentEnd.UTC(), s.CloseReason, s.Version,
				PropertiesColumn(s.Properties),
			); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("user_segments chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// InsertUserDeltas batch-inserts into user_minute_deltas (FQN).
func InsertUserDeltas(ctx context.Context, conn driver.Conn, table string, rows []models.UserMinuteDelta) error {
	if len(rows) == 0 {
		return nil
	}
	stmt := fmt.Sprintf("INSERT INTO %s (minute, user_segment_id, delta)", table)
	for start := 0; start < len(rows); start += insertChunkSize {
		end := start + insertChunkSize
		if end > len(rows) {
			end = len(rows)
		}
		batch, err := conn.PrepareBatch(ctx, stmt)
		if err != nil {
			return err
		}
		for _, d := range rows[start:end] {
			if err := batch.Append(d.Minute.UTC(), d.UserSegmentID, d.Delta); err != nil {
				return err
			}
		}
		if err := batch.Send(); err != nil {
			return fmt.Errorf("user_deltas chunk [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

// StageAndReplace performs an atomic per-partition swap: it builds the new data
// in a staging table (same engine/partitioning, created AS the target) and then
// ALTER TABLE ... REPLACE PARTITION ... FROM staging for each affected day.
//
// This is deliberately NOT drop-then-insert: DROP PARTITION followed by INSERT
// leaves a window where the day's partition is empty, so a concurrent dashboard
// or benchmark query reads missing data. REPLACE PARTITION is a single atomic
// metadata swap — readers see either the old partition or the new one, never a
// gap. It is also idempotent: re-running rebuilds staging and re-swaps.
//
// `insert` receives the staging table's FQN and must write the new rows there.
func StageAndReplace(ctx context.Context, conn driver.Conn, database, table string, days []string, insert func(stagingFQN string) error) error {
	if len(days) == 0 {
		return nil
	}
	staging := fmt.Sprintf("%s.`_stg_%s`", database, table)
	target := fmt.Sprintf("%s.%s", database, table)

	if err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s AS %s", staging, target)); err != nil {
		return fmt.Errorf("create staging %s: %w", staging, err)
	}
	// Best-effort cleanup; leftover staging never affects the served tables.
	defer func() { _ = conn.Exec(ctx, "DROP TABLE IF EXISTS "+staging) }()

	if err := conn.Exec(ctx, "TRUNCATE TABLE "+staging); err != nil {
		return fmt.Errorf("truncate staging %s: %w", staging, err)
	}
	if err := insert(staging); err != nil {
		return fmt.Errorf("stage insert: %w", err)
	}
	for _, d := range days {
		sql := fmt.Sprintf("ALTER TABLE %s REPLACE PARTITION '%s' FROM %s", target, d, staging)
		if err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("replace %s partition %s: %w", target, d, err)
		}
	}
	return nil
}

// PartitionDays returns the distinct toYYYYMMDD partition keys covering the times.
func PartitionDays(times ...time.Time) []string {
	seen := map[string]struct{}{}
	for _, t := range times {
		seen[t.UTC().Format("20060102")] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
