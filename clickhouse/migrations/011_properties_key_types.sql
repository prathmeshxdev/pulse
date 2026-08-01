-- Daily append catalog of dynamic property keys → types (last 24h sample only).
-- Replaces the 15-minute full-table scan; types accumulate across appends.
-- Manual catch-up after bulk load: SYSTEM REFRESH VIEW sony_liv.mv_refresh_properties_key_mappings;

DROP VIEW IF EXISTS sony_liv.mv_refresh_properties_key_types;
DROP TABLE IF EXISTS sony_liv.properties_key_types;

CREATE TABLE IF NOT EXISTS sony_liv.properties_key_mappings
(
    source       LowCardinality(String),
    paths        Map(String, Array(String)),
    refreshed_at DateTime DEFAULT now()
)
ENGINE = MergeTree
ORDER BY (source, refreshed_at)
SETTINGS index_granularity = 8192;

CREATE MATERIALIZED VIEW IF NOT EXISTS sony_liv.mv_refresh_properties_key_mappings
REFRESH EVERY 1 DAY OFFSET 22 HOUR RANDOMIZE FOR 1 HOUR
APPEND TO sony_liv.properties_key_mappings
AS
SELECT
    source,
    distinctJSONPathsAndTypes(properties) AS paths,
    now() AS refreshed_at
FROM
(
    SELECT 'raw_events' AS source, properties
    FROM sony_liv.raw_events
    WHERE event_timestamp >= (now() - toIntervalHour(24))
      AND NOT empty(JSONDynamicPaths(properties))

    UNION ALL

    SELECT 'session_active_segments' AS source, properties
    FROM sony_liv.session_active_segments FINAL
    WHERE segment_start >= (now() - toIntervalHour(24))
      AND NOT empty(JSONDynamicPaths(properties))
)
GROUP BY source
SETTINGS
    max_threads = 4,
    max_execution_time = 3600,
    max_memory_usage = 10000000000;
