-- Optional wide rollup (FINAL_PLAN §4.6 / SCHEMA_AND_DDL §008): the 100× answer.
-- Dimensions are denormalized ONTO the delta rows, so filtered queries scan this
-- single table (no segment semi-join). The narrow minute_deltas + semi-join stays
-- the default; this is an opt-in accelerator, populated by the same pipeline.
--
-- CORRECTNESS NOTE: this is a SummingMergeTree, so EVERY dimension must be in the
-- ORDER BY. Summing collapses rows with an equal sorting key and sums `delta`; a
-- dimension left out of the key would sum across distinct dim values → wrong
-- concurrency. That is also why this is a wide "cube" (limited summing benefit)
-- and why it is only earned at scale — see the trade-off table in SCHEMA_AND_DDL.
CREATE TABLE IF NOT EXISTS sony_liv.concurrency_minute_serving
(
    minute            DateTime('UTC') CODEC(DoubleDelta, ZSTD(1)),
    platform          LowCardinality(String),
    country           LowCardinality(String),
    content_id        UInt64,
    app_version       LowCardinality(String),
    audio_language    LowCardinality(String),
    subtitle_language LowCardinality(String),
    player_version    LowCardinality(String),
    delta             Int64 CODEC(ZSTD(1))
)
ENGINE = SummingMergeTree
PARTITION BY toYYYYMMDD(minute)
ORDER BY (minute, platform, country, content_id, app_version, audio_language, subtitle_language, player_version)
SETTINGS index_granularity = 8192;
