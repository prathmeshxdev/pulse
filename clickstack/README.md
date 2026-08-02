# ClickStack — OTLP traces / metrics / logs → ClickHouse Cloud

Pulse emits OpenTelemetry **traces**, **metrics**, and **logs** from the API
(`concurrency.chart`, `concurrency.breakdown`, HTTP routes) and batch commands
(`loadraw`, `build_segments`, `pipeline`). The **ClickStack Cloud collector**
forwards them to ClickHouse Cloud tables `default.otel_*`.

## Run (Podman / Docker)

```bash
# From repo root — derives observability.env from CLICKHOUSE_DSN:
./clickhouse/scripts/sync_librechat_env.sh

podman-compose --profile observability up -d clickstack   # OTLP :4317 / :4318

# Point Pulse at the collector (required):
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
# host API:
cd backend && go run ./cmd/server
# or compose backend with the same env
```

## What gets exported

| Signal | Examples | Cloud table |
|--------|----------|-------------|
| Traces | `concurrency.chart`, `concurrency.breakdown`, `http.POST /…` | `otel_traces` |
| Span events | `db.query`, `request.complete`, `error`, `log` | `otel_traces.Events.*` |
| Metrics | `pulse.http.requests`, `pulse.http.errors`, `pulse.http.duration_ms`, `pulse.clickhouse.duration_ms` | `otel_metrics_sum` / `otel_metrics_histogram` |
| Logs | `chart ok`, `request failed`, ClickHouse errors | `otel_logs` |

Span attributes include `grain`, `unit`, `dimension`, `filters`, `peak`, `avg`,
`duration_ms`, `http.status_code`, `db.duration_ms`.

## Populate quickly

```bash
# Generate chart + breakdown traffic
for i in 1 2 3 4 5; do
  curl -s http://127.0.0.1:8080/api/v1/concurrency/chart \
    -H 'Content-Type: application/json' \
    -d '{"start":"2026-07-31T00:00:00Z","end":"2026-08-01T00:00:00Z","grain":"minute","metric":"summary","filters":[]}'
  curl -s http://127.0.0.1:8080/api/v1/concurrency/breakdown \
    -H 'Content-Type: application/json' \
    -d '{"start":"2026-07-31T00:00:00Z","end":"2026-08-01T00:00:00Z","dimension":"platform","limit":5}'
done
# Force a client error (shows up in error metrics/logs)
curl -s http://127.0.0.1:8080/api/v1/concurrency/chart \
  -H 'Content-Type: application/json' -d '{"start":"bad"}'
```

Wait ~10–15s for metric export interval, then query Cloud.

## Dashboards (SQL views)

Apply once:

```bash
# paste clickstack/dashboards.sql into ClickHouse Cloud SQL console
# or via pipeline -exec / clickhouse-client
```

| View | Purpose |
|------|---------|
| `v_pulse_latency_1h` | p50/p90/p99 by span (chart vs breakdown) |
| `v_pulse_errors_24h` | Error % by route (5m buckets) |
| `v_pulse_slow_queries_6h` | Spans >500ms with grain/unit/peak |
| `v_pulse_db_events_1h` | ClickHouse query timing events |
| `v_pulse_rps_24h` | Charts / breakdowns / HTTP volume |
| `v_pulse_logs_1h` | Application logs (INFO/ERROR) |
| `v_pulse_metric_latency` | Histogram metric samples |
| `v_pulse_metric_counters` | Request + error counters |

```sql
SELECT * FROM default.v_pulse_latency_1h;
SELECT * FROM default.v_pulse_rps_24h LIMIT 60;
SELECT * FROM default.v_pulse_logs_1h LIMIT 50;
```

## Recommended dashboard panels

1. **Latency** — p50/p90 of `concurrency.chart` vs `concurrency.breakdown`
2. **Traffic** — charts/min + breakdowns/min (`v_pulse_rps_24h`)
3. **Errors** — 4xx/5xx rate + error log stream
4. **Slow queries** — table of spans >500ms with filters/peak
5. **CH query time** — `pulse.clickhouse.duration_ms` histogram / `db.query` events
6. **Top dimensions** — breakdown calls by `SpanAttributes['dimension']`

## Submission evidence (HyperDX exports)

Committed CSV search exports from the ClickStack / HyperDX UI:

- [`evidence/clickstack/hyperdx_traces_2026-08-02.csv`](../evidence/clickstack/hyperdx_traces_2026-08-02.csv) — `concurrency.chart`, `concurrency.breakdown`, HTTP spans
- [`evidence/clickstack/hyperdx_logs_2026-08-02.csv`](../evidence/clickstack/hyperdx_logs_2026-08-02.csv) — `chart ok`, `breakdown ok`, `request ok`

See [`evidence/clickstack/README.md`](../evidence/clickstack/README.md).

## Local HyperDX (legacy)

`clickstack/docker-compose.yml` still runs the all-in-one HyperDX UI on :8081 for
local-only demos. Prefer the Cloud collector above for hackathon parity with
ClickHouse Cloud.
