CREATE TABLE IF NOT EXISTS sony_liv.content_metadata
(
    content_id UInt64,
    title      String,
    video_type LowCardinality(String),
    category   LowCardinality(String),
    loaded_at  DateTime('UTC') DEFAULT now()
)
ENGINE = MergeTree
ORDER BY content_id
SETTINGS index_granularity = 8192;
