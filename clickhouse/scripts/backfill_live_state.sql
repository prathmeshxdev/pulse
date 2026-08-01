-- Backfill session_live_state from existing raw_events.
-- The MV (migration 009) maintains this live on direct INSERTs (streaming path),
-- but the idempotent batch load uses staging + REPLACE PARTITION, which does NOT
-- fire MVs. Run this once after a batch load so the live gauge reflects it.
-- (On the training/unseen day every session is closed, so active_now = 0 — correct.)

INSERT INTO sony_liv.session_live_state
SELECT
    video_session_id,
    maxState(toUInt8(event_type = 'VideoSessionEnd')),
    argMaxState(toUInt8(event_type != 'AppBackgrounded'),
        if(event_type IN ('AppBackgrounded','AppForegrounded','VideoSessionStart'), event_timestamp, toDateTime64('1970-01-01 00:00:00',3,'UTC'))),
    argMaxState(multiIf(event_type='VideoHeartbeat' AND event IN ('pause','speed-pause','AdPause'), toUInt8(0),
                        event_type='VideoPlay' OR (event_type='VideoHeartbeat' AND event IN ('resume','speed-resume','AdResume')), toUInt8(1), toUInt8(0)),
        if(event_type='VideoPlay' OR (event_type='VideoHeartbeat' AND event IN ('pause','speed-pause','AdPause','resume','speed-resume','AdResume')), event_timestamp, toDateTime64('1970-01-01 00:00:00',3,'UTC'))),
    maxIfState(event_timestamp, toUInt8(event_type IN ('VideoHeartbeat','VideoPlay'))),
    argMaxState(platform, event_timestamp),
    argMaxState(country, event_timestamp),
    argMaxState(content_id, event_timestamp)
FROM sony_liv.raw_events
GROUP BY video_session_id;
