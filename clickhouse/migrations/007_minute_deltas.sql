CREATE TABLE IF NOT EXISTS sony_liv.minute_deltas
(
    minute     DateTime('UTC'),
    segment_id UInt64,
    delta      Int64
)
ENGINE = SummingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, segment_id)
SETTINGS index_granularity = 8192;
