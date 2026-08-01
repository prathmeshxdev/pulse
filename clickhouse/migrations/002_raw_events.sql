CREATE TABLE IF NOT EXISTS sony_liv.raw_events
(
    video_session_id    String,
    user_id             String,
    content_id          UInt64,
    event_type          LowCardinality(String),
    event               LowCardinality(String),
    event_timestamp     DateTime64(3, 'UTC') CODEC(Delta, ZSTD(1)),
    platform            LowCardinality(String),
    app_version         LowCardinality(String),
    country             LowCardinality(String),
    audio_language      LowCardinality(String),
    subtitle_language   LowCardinality(String),
    player_version      LowCardinality(String),
    session_start_epoch DateTime64(3, 'UTC') CODEC(Delta, ZSTD(1)),

    properties          JSON DEFAULT '{}' CODEC(ZSTD(1)),

    ingest_batch_id     UUID     DEFAULT generateUUIDv4(),
    ingested_at         DateTime64(3, 'UTC') DEFAULT now64(3) CODEC(Delta, ZSTD(1))
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(event_timestamp)
ORDER BY (video_session_id, event_timestamp, event_type, event)
SETTINGS
    index_granularity = 8192,
    object_serialization_version = 'v3',
    object_shared_data_serialization_version = 'advanced',
    object_shared_data_buckets_for_compact_part = 16,
    object_shared_data_buckets_for_wide_part = 64,
    object_shared_data_serialization_version_for_zero_level_parts = 'map_with_buckets';
