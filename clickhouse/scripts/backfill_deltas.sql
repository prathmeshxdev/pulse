-- Emit any-overlap minute deltas from session_active_segments.
-- Idempotency: DROP PARTITION for affected days before running (SCHEMA_AND_DDL §4.3 mechanism 4).

INSERT INTO sony_liv.minute_deltas
SELECT
    toStartOfMinute(segment_start) AS minute,
    segment_id,
    toInt64(1) AS delta
FROM sony_liv.session_active_segments FINAL
WHERE segment_end > segment_start

UNION ALL

SELECT
    toStartOfMinute(segment_end - toIntervalMillisecond(1)) + toIntervalMinute(1) AS minute,
    segment_id,
    toInt64(-1) AS delta
FROM sony_liv.session_active_segments FINAL
WHERE segment_end > segment_start;
