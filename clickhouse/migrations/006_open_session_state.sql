CREATE TABLE IF NOT EXISTS sony_liv.open_session_state
(
    video_session_id String,
    user_id String,
    content_id UInt64,
    platform LowCardinality(String),
    country LowCardinality(String),
    app_version LowCardinality(String),
    audio_language LowCardinality(String),
    subtitle_language LowCardinality(String),
    player_version LowCardinality(String),

    segment_start DateTime64(3, 'UTC'),
    segment_end DateTime64(3, 'UTC'),

    last_event_type LowCardinality(String),
    last_event_timestamp DateTime64(3, 'UTC'),
    is_session_closed UInt8 DEFAULT 0,

    computed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(computed_at)
ORDER BY video_session_id
SETTINGS index_granularity = 8192;
