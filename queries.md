# Pulse — Query catalog

Batch insert shapes and serving-layer analytics SQL.
Inserts: [`backend/internal/chclient/insert.go`](backend/internal/chclient/insert.go).
Chart compiler: [`backend/internal/concurrency/query.go`](backend/internal/concurrency/query.go).

---

## Batch inserts

Executed by the Go pipeline (`loadraw`, `loadcontent`, `build_segments`, `build_user_segments`). Chunks of 100k rows.

### Raw events

```sql
INSERT INTO sony_liv.raw_events
    (video_session_id, user_id, content_id, event_type, event, event_timestamp,
     platform, app_version, country, audio_language, subtitle_language,
     player_version, session_start_epoch, properties)
VALUES (...);
```

### Content metadata

```sql
INSERT INTO sony_liv.content_metadata
    (content_id, title, video_type, category, show_name)
VALUES (...);
```

### Session active segments

```sql
INSERT INTO sony_liv.session_active_segments
    (segment_id, video_session_id, user_id, content_id, platform, country,
     app_version, audio_language, subtitle_language, player_version,
     segment_start, segment_end, is_final, close_reason, version, properties)
VALUES (...);
```

### Minute deltas (narrow sweep-line)

```sql
INSERT INTO sony_liv.minute_deltas (minute, segment_id, delta)
VALUES (...);
```

Edge semantics ([`backfill_deltas.sql`](clickhouse/scripts/backfill_deltas.sql)):

```sql
-- +1 at segment start minute
SELECT toStartOfMinute(segment_start) AS minute, segment_id, toInt64(1) AS delta
FROM sony_liv.session_active_segments FINAL
WHERE segment_end > segment_start

UNION ALL

-- -1 at minute after last active minute
SELECT toStartOfMinute(segment_end - toIntervalMillisecond(1)) + toIntervalMinute(1),
       segment_id, toInt64(-1)
FROM sony_liv.session_active_segments FINAL
WHERE segment_end > segment_start;
```

### Wide rollup (optional)

```sql
INSERT INTO sony_liv.concurrency_minute_serving
    (minute, platform, country, content_id, app_version, audio_language,
     subtitle_language, player_version, delta)
VALUES (...);
```

### User-level serving

```sql
INSERT INTO sony_liv.user_active_segments (...columns...) VALUES (...);
INSERT INTO sony_liv.user_minute_deltas (minute, user_segment_id, delta) VALUES (...);
```

### Atomic partition swap (production rebuild)

```sql
CREATE TABLE IF NOT EXISTS sony_liv.`_stg_minute_deltas` AS sony_liv.minute_deltas;
TRUNCATE TABLE sony_liv.`_stg_minute_deltas`;
-- INSERT into staging ...
ALTER TABLE sony_liv.minute_deltas REPLACE PARTITION '20260517' FROM sony_liv.`_stg_minute_deltas`;
DROP TABLE IF EXISTS sony_liv.`_stg_minute_deltas`;
```

Implemented in [`StageAndReplace`](backend/internal/chclient/insert.go).

---

## Analytics

### Concurrency chart

Normative template for `POST /api/v1/concurrency/chart`, `cmd/bench`, and the dashboard.
Static reference: [`peak_avg_minute.sql`](clickhouse/queries/benchmark/peak_avg_minute.sql) (production adds `open_edges`).

**Session-level, unfiltered, summary** — `range_start`, `range_end`, 72h lookback:

```sql
WITH
    toDateTime('2023-05-17 05:23:00', 'UTC') AS range_start,
    toDateTime('2026-08-01 11:10:00', 'UTC') AS range_end,

    open_edges AS (
        SELECT toStartOfMinute(segment_start) AS minute, 1 AS delta
        FROM sony_liv.session_active_segments FINAL
        WHERE close_reason = ''
          AND segment_end > range_start - INTERVAL 72 HOUR
          AND segment_start < range_end
        UNION ALL
        SELECT toStartOfMinute(subtractMilliseconds(segment_end, 1)) + toIntervalMinute(1),
               -1 AS delta
        FROM sony_liv.session_active_segments FINAL
        WHERE close_reason = ''
          AND segment_end > range_start - INTERVAL 72 HOUR
          AND segment_start < range_end
    ),

    opening AS (
        SELECT sum(delta) AS c0 FROM (
            SELECT delta FROM sony_liv.minute_deltas
            WHERE minute >= range_start - INTERVAL 72 HOUR AND minute < range_start
            UNION ALL
            SELECT delta FROM open_edges
            WHERE minute >= range_start - INTERVAL 72 HOUR AND minute < range_start
        )
    ),

    net AS (
        SELECT minute, sum(delta) AS net FROM (
            SELECT minute, delta FROM sony_liv.minute_deltas
            WHERE minute >= range_start AND minute < range_end
            UNION ALL
            SELECT minute, delta FROM open_edges
            WHERE minute >= range_start AND minute < range_end
        )
        GROUP BY minute
    ),

    grid AS (
        SELECT range_start + toIntervalMinute(number) AS minute
        FROM numbers(dateDiff('minute', range_start, range_end))
    ),

    curve AS (
        SELECT
            g.minute,
            ifNull((SELECT c0 FROM opening), 0)
                + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
        FROM grid AS g
        LEFT JOIN net AS n ON g.minute = n.minute
    )

SELECT
    max(concurrency) AS peak_concurrency,
    avg(concurrency) AS avg_concurrency
FROM curve;
```

**With dimension filters** — add semi-join `sel` and restrict deltas:

```sql
sel AS (
    SELECT segment_id
    FROM sony_liv.session_active_segments FINAL
    WHERE segment_start < range_end
      AND segment_end   > range_start
      AND segment_start >= range_start - INTERVAL 72 HOUR
      AND platform = 'ANDROID_PHONE'
      -- AND dictGet('sony_liv.content_dict', 'video_type', content_id) = 'live'
      -- AND toString(properties.network_type) = 'wifi'
),
-- opening / net: AND segment_id IN (SELECT segment_id FROM sel)
```

| Dimension kind | Example predicate |
|----------------|-------------------|
| Segment column | `platform = 'ANDROID_PHONE'` |
| Content dict | `dictGet('sony_liv.content_dict', 'video_type', content_id) = 'live'` |
| JSON property | `toString(properties.network_type) = 'wifi'` |
| Numeric property | `properties.bandwidth_mbps = 100` |

| `metric` | Final SELECT over `curve` |
|----------|---------------------------|
| `summary` | `max(concurrency), avg(concurrency)` |
| `peak` | `max(concurrency) AS peak_concurrency` |
| `avg` | `avg(concurrency) AS avg_concurrency` |
| `timeseries` + `minute` | `minute, concurrency ORDER BY minute` |
| `timeseries` + `hour` | `toStartOfHour(minute) AS bucket, max, avg GROUP BY bucket` |
| `timeseries` + `day` | `toStartOfDay(minute) AS bucket, max, avg GROUP BY bucket` |

**User-level unit** — same template on `user_active_segments` / `user_minute_deltas` / `user_segment_id`.

### Wide rollup path

When all filters hit rollup columns, no segment semi-join ([`BuildRollupQuery`](backend/internal/concurrency/query.go)):

```sql
WITH
    opening AS (
        SELECT sum(delta) AS c0
        FROM sony_liv.concurrency_minute_serving
        WHERE minute >= range_start - INTERVAL 72 HOUR
          AND minute < range_start
          AND platform = 'ANDROID_PHONE'
    ),
    net AS (
        SELECT minute, sum(delta) AS net
        FROM sony_liv.concurrency_minute_serving
        WHERE minute >= range_start AND minute < range_end
          AND platform = 'ANDROID_PHONE'
        GROUP BY minute
    ),
    grid AS (
        SELECT range_start + toIntervalMinute(number) AS minute
        FROM numbers(dateDiff('minute', range_start, range_end))
    ),
    curve AS (
        SELECT g.minute,
               ifNull((SELECT c0 FROM opening), 0)
                   + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
        FROM grid g LEFT JOIN net n ON g.minute = n.minute
    )
SELECT max(concurrency), avg(concurrency) FROM curve;
```

### Breakdown

`POST /api/v1/concurrency/breakdown` — top-N values, then one chart query per value:

```sql
-- Step 1: top values by segment count
SELECT platform AS v
FROM sony_liv.session_active_segments FINAL
WHERE platform != ''
GROUP BY v
ORDER BY count() DESC
LIMIT 10;

-- Step 2: full chart query (§ Concurrency chart) with platform = '<value>' filter
```

### Live gauge

`GET /api/v1/concurrency/live?source=mv`:

```sql
WITH (SELECT max(event_timestamp) FROM sony_liv.raw_events) AS T
SELECT
    countIf(
        NOT closed AND fg = 1 AND playing = 1
        AND dateDiff('second', last_hb, T) <= 48
    ) AS active_now,
    countIf(NOT closed) AS open_sessions
FROM (
    SELECT
        video_session_id,
        maxMerge(closed) AS closed,
        argMaxMerge(fg) AS fg,
        argMaxMerge(playing) AS playing,
        maxIfMerge(last_hb) AS last_hb
    FROM sony_liv.session_live_state
    GROUP BY video_session_id
);
```

Primary live path uses Redis (`streamd`); this MV query is the fallback.

### Schema discovery

| Endpoint | Query |
|----------|-------|
| `GET /api/v1/schema/window` | `SELECT min(minute) AS start, max(minute) + toIntervalMinute(1) AS end FROM sony_liv.minute_deltas` |
| `GET /api/v1/schema/values?dimension=platform` | `SELECT DISTINCT platform AS v FROM sony_liv.session_active_segments FINAL WHERE platform != '' ORDER BY v LIMIT 500` |
| `GET /api/v1/schema/values?dimension=title` | `SELECT DISTINCT dictGet('sony_liv.content_dict', 'title', content_id) AS v FROM sony_liv.content_metadata WHERE title != '' ORDER BY v LIMIT 500` |

Dynamic property keys (filter compilation):

```sql
SELECT k AS key, any(arrayElement(v, 1)) AS ch_type
FROM sony_liv.properties_key_mappings
ARRAY JOIN mapKeys(paths) AS k, paths[k] AS v
GROUP BY k;
```
