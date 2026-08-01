CREATE TABLE IF NOT EXISTS sony_liv.minute_deltas
(
    -- Codecs (CH best practice): minute is monotonic → DoubleDelta; the narrow
    -- +1/-1 deltas and repeated segment_ids compress hard under ZSTD.
    minute     DateTime('UTC') CODEC(DoubleDelta, ZSTD(1)),
    segment_id UInt64          CODEC(ZSTD(1)),
    delta      Int64           CODEC(ZSTD(1))
)
ENGINE = SummingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, segment_id)
SETTINGS index_granularity = 8192;
