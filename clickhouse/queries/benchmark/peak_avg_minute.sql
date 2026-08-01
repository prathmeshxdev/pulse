-- Normative peak/avg template (SCHEMA_AND_DDL.md).
-- Parameters: {start}, {end}, {max_segment_span_hours}, optional dimension predicates.

WITH
    toDateTime({start:String}, 'UTC') AS range_start,
    toDateTime({end:String}, 'UTC')   AS range_end,

    sel AS (
        SELECT segment_id
        FROM sony_liv.session_active_segments FINAL
        WHERE segment_start < range_end
          AND segment_end   > range_start
          AND segment_start >= range_start - INTERVAL {max_segment_span_hours:UInt32} HOUR
          -- AND platform = {platform:String}
          -- AND country  = {country:String}
    ),

    opening AS (
        SELECT sum(delta) AS c0
        FROM sony_liv.minute_deltas
        WHERE minute >= range_start - INTERVAL {max_segment_span_hours:UInt32} HOUR
          AND minute <  range_start
          AND segment_id IN (SELECT segment_id FROM sel)
    ),

    net AS (
        SELECT minute, sum(delta) AS net
        FROM sony_liv.minute_deltas
        WHERE minute >= range_start AND minute < range_end
          AND segment_id IN (SELECT segment_id FROM sel)
        GROUP BY minute
    ),

    grid AS (
        SELECT range_start + toIntervalMinute(number) AS minute
        FROM numbers(dateDiff('minute', range_start, range_end))
    ),

    curve AS (
        SELECT
            g.minute AS minute,
            ifNull((SELECT c0 FROM opening), 0)
                + sum(ifNull(n.net, 0)) OVER (ORDER BY g.minute) AS concurrency
        FROM grid AS g
        LEFT JOIN net AS n ON g.minute = n.minute
    )

SELECT
    max(concurrency) AS peak_concurrency,
    avg(concurrency) AS avg_concurrency
FROM curve;
