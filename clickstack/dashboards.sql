-- Pulse × ClickStack dashboards — run in ClickHouse Cloud SQL console.
-- Data lands in default.otel_* via the clickstack-otel-collector (:4318).
-- ServiceName = 'pulse-concurrency-api'

-- =============================================================================
-- 1) API latency overview (p50 / p90 / p99) — last 1h
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_latency_1h AS
SELECT
    SpanName,
    count() AS requests,
    round(quantile(0.50)(Duration) / 1e6, 2) AS p50_ms,
    round(quantile(0.90)(Duration) / 1e6, 2) AS p90_ms,
    round(quantile(0.99)(Duration) / 1e6, 2) AS p99_ms,
    round(avg(Duration) / 1e6, 2) AS avg_ms,
    countIf(StatusCode = 'Error') AS errors
FROM default.otel_traces
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 1 HOUR
  AND SpanName LIKE 'concurrency.%'
GROUP BY SpanName
ORDER BY requests DESC;

-- =============================================================================
-- 2) Error rate by route — last 24h (5m buckets)
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_errors_24h AS
SELECT
    toStartOfFiveMinutes(Timestamp) AS bucket,
    SpanAttributes['http.route'] AS route,
    count() AS requests,
    countIf(StatusCode = 'Error') AS errors,
    round(100 * errors / requests, 2) AS error_pct
FROM default.otel_traces
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 24 HOUR
  AND startsWith(SpanName, 'http.')
GROUP BY bucket, route
ORDER BY bucket DESC, errors DESC;

-- =============================================================================
-- 3) Slow queries (>500ms) — last 6h
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_slow_queries_6h AS
SELECT
    Timestamp,
    SpanName,
    round(Duration / 1e6, 2) AS duration_ms,
    SpanAttributes['grain'] AS grain,
    SpanAttributes['unit'] AS unit,
    SpanAttributes['dimension'] AS dimension,
    SpanAttributes['peak'] AS peak,
    StatusCode,
    StatusMessage,
    TraceId
FROM default.otel_traces
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 6 HOUR
  AND Duration > 500e6
ORDER BY Timestamp DESC
LIMIT 200;

-- =============================================================================
-- 4) ClickHouse query timing from span events (db.query)
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_db_events_1h AS
SELECT
    Timestamp,
    SpanName,
    Events.Name AS event_name,
    Events.Attributes AS event_attrs,
    TraceId
FROM default.otel_traces
ARRAY JOIN Events.Name, Events.Attributes
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 1 HOUR
  AND event_name IN ('db.query', 'error', 'log', 'request.complete')
ORDER BY Timestamp DESC
LIMIT 500;

-- =============================================================================
-- 5) Request volume — last 24h (1m buckets)
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_rps_24h AS
SELECT
    toStartOfMinute(Timestamp) AS minute,
    countIf(SpanName = 'concurrency.chart') AS charts,
    countIf(SpanName = 'concurrency.breakdown') AS breakdowns,
    countIf(startsWith(SpanName, 'http.')) AS http_total,
    countIf(StatusCode = 'Error') AS errors
FROM default.otel_traces
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 24 HOUR
GROUP BY minute
ORDER BY minute DESC;

-- =============================================================================
-- 6) OTLP application logs (severity + body)
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_logs_1h AS
SELECT
    Timestamp,
    SeverityText,
    Body,
    LogAttributes,
    TraceId,
    SpanId
FROM default.otel_logs
WHERE ServiceName = 'pulse-concurrency-api'
  AND Timestamp > now() - INTERVAL 1 HOUR
ORDER BY Timestamp DESC
LIMIT 500;

-- =============================================================================
-- 7) Metrics: HTTP duration histogram (from OTLP metrics)
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_metric_latency AS
SELECT
    TimeUnix,
    MetricName,
    Attributes['http.route'] AS route,
    Attributes['status'] AS status,
    Count,
    Sum,
    BucketCounts,
    ExplicitBounds
FROM default.otel_metrics_histogram
WHERE ServiceName = 'pulse-concurrency-api'
  AND MetricName = 'pulse.http.duration_ms'
ORDER BY TimeUnix DESC
LIMIT 200;

-- =============================================================================
-- 8) Metrics: request / error counters
-- =============================================================================
CREATE OR REPLACE VIEW default.v_pulse_metric_counters AS
SELECT
    TimeUnix,
    MetricName,
    Attributes['http.route'] AS route,
    Attributes['status'] AS status,
    Value
FROM default.otel_metrics_sum
WHERE ServiceName = 'pulse-concurrency-api'
  AND MetricName IN ('pulse.http.requests', 'pulse.http.errors')
ORDER BY TimeUnix DESC
LIMIT 200;

-- Quick checks (ad-hoc):
-- SELECT * FROM default.v_pulse_latency_1h;
-- SELECT * FROM default.v_pulse_errors_24h LIMIT 50;
-- SELECT * FROM default.v_pulse_slow_queries_6h LIMIT 50;
-- SELECT * FROM default.v_pulse_rps_24h LIMIT 60;
-- SELECT * FROM default.v_pulse_logs_1h LIMIT 50;
