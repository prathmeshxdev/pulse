-- Emit any-overlap minute deltas from session_active_segments.
--
-- Idempotency without a read gap: rather than DROP PARTITION + INSERT (which
-- leaves the day empty to concurrent readers), the Go loader (cmd/build_segments)
-- builds these rows in a staging table and does ALTER TABLE minute_deltas
-- REPLACE PARTITION '<day>' FROM staging — an atomic per-day swap. This raw
-- INSERT is the pure-SQL reference; for a live table, stage-and-replace instead.

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
