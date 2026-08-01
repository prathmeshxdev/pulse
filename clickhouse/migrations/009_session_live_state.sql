-- Real-time "active viewers now" via a materialized view (answers the streaming
-- half of the problem: dedup + summing as events arrive).
--
-- Why an MV works HERE but not for the historical curve: the minute curve needs
-- ordered per-session intervals (a state machine — an MV can't see session history).
-- But CURRENT state per session is reconstructable with argMax/max over the right
-- event subset, and those are:
--   * idempotent   — re-inserting a duplicate event doesn't change max/argMax
--   * order-free   — argMax picks by event_timestamp, so late / out-of-order arrivals
--                    land correctly (a stale event never overrides a newer state)
-- So this updates on every INSERT with no reconcile. It is a live gauge that
-- COMPLEMENTS minute_deltas (which still owns minute/hour/day peak & average).

CREATE TABLE IF NOT EXISTS sony_liv.session_live_state
(
    video_session_id String,
    closed   AggregateFunction(max,    UInt8),
    fg       AggregateFunction(argMax,  UInt8, DateTime64(3, 'UTC')),
    playing  AggregateFunction(argMax,  UInt8, DateTime64(3, 'UTC')),
    last_hb  AggregateFunction(maxIf,   DateTime64(3, 'UTC'), UInt8),
    platform AggregateFunction(argMax,  LowCardinality(String), DateTime64(3, 'UTC')),
    country  AggregateFunction(argMax,  LowCardinality(String), DateTime64(3, 'UTC')),
    content  AggregateFunction(argMax,  UInt64, DateTime64(3, 'UTC'))
)
ENGINE = AggregatingMergeTree
ORDER BY video_session_id
SETTINGS index_granularity = 8192;

-- The MV maintains the states on every insert into raw_events (streaming path).
-- `z` is the epoch-0 tiebreak so non-state events never win the argMax.
CREATE MATERIALIZED VIEW IF NOT EXISTS sony_liv.mv_session_live_state
TO sony_liv.session_live_state AS
SELECT
    video_session_id,
    maxState(toUInt8(event_type = 'VideoSessionEnd')) AS closed,
    argMaxState(
        toUInt8(event_type != 'AppBackgrounded'),
        if(event_type IN ('AppBackgrounded', 'AppForegrounded', 'VideoSessionStart'),
           event_timestamp, toDateTime64('1970-01-01 00:00:00', 3, 'UTC'))) AS fg,
    argMaxState(
        multiIf(event_type = 'VideoHeartbeat' AND event IN ('pause','speed-pause','AdPause'), toUInt8(0),
                event_type = 'VideoPlay' OR (event_type = 'VideoHeartbeat' AND event IN ('resume','speed-resume','AdResume')), toUInt8(1),
                toUInt8(0)),
        if(event_type = 'VideoPlay' OR (event_type = 'VideoHeartbeat' AND event IN ('pause','speed-pause','AdPause','resume','speed-resume','AdResume')),
           event_timestamp, toDateTime64('1970-01-01 00:00:00', 3, 'UTC'))) AS playing,
    maxIfState(event_timestamp, toUInt8(event_type IN ('VideoHeartbeat', 'VideoPlay'))) AS last_hb,
    argMaxState(platform,   event_timestamp) AS platform,
    argMaxState(country,    event_timestamp) AS country,
    argMaxState(content_id, event_timestamp) AS content
FROM sony_liv.raw_events
GROUP BY video_session_id;
