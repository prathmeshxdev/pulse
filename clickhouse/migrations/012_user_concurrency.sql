-- Session-independent (user-level) concurrency: merged active intervals per user_id
-- and narrow sweep-line deltas. Built from session_active_segments via island merge.

CREATE TABLE IF NOT EXISTS sony_liv.user_active_segments
(
    user_segment_id   UInt64,
    user_id           String,

    content_id        UInt64,
    platform          LowCardinality(String),
    country           LowCardinality(String),
    app_version       LowCardinality(String),
    audio_language    LowCardinality(String),
    subtitle_language LowCardinality(String),
    player_version    LowCardinality(String),

    video_type        LowCardinality(String),
    category          LowCardinality(String),

    segment_start     DateTime64(3, 'UTC'),
    segment_end       DateTime64(3, 'UTC'),

    close_reason      LowCardinality(String) DEFAULT '',

    properties        JSON DEFAULT '{}' CODEC(ZSTD(1)),

    version           UInt64 DEFAULT 1,
    computed_at       DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(version)
PARTITION BY toYYYYMMDD(segment_start)
ORDER BY (user_segment_id)
SETTINGS
    index_granularity = 8192,
    object_serialization_version = 'v3',
    object_shared_data_serialization_version = 'advanced',
    object_shared_data_buckets_for_compact_part = 16,
    object_shared_data_buckets_for_wide_part = 64,
    object_shared_data_serialization_version_for_zero_level_parts = 'map_with_buckets';

ALTER TABLE sony_liv.user_active_segments
    ADD INDEX IF NOT EXISTS idx_user user_id TYPE bloom_filter(0.01) GRANULARITY 4;

ALTER TABLE sony_liv.user_active_segments
    ADD INDEX IF NOT EXISTS idx_user_span (segment_start, segment_end) TYPE minmax GRANULARITY 4;

CREATE TABLE IF NOT EXISTS sony_liv.user_minute_deltas
(
    minute            DateTime('UTC') CODEC(DoubleDelta, ZSTD(1)),
    user_segment_id   UInt64          CODEC(ZSTD(1)),
    delta             Int64           CODEC(ZSTD(1))
)
ENGINE = SummingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, user_segment_id)
SETTINGS index_granularity = 8192;
