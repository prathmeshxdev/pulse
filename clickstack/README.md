# ClickStack — OTLP traces → ClickHouse Cloud

Pulse emits OpenTelemetry spans from the API (`concurrency.chart`) and batch
commands (`loadraw`, `build_segments`, `pipeline`). The **ClickStack Cloud
collector** forwards them to your ClickHouse Cloud service — same pattern as
using Cloud for data instead of a local HyperDX stack.

## Run (Podman / Docker)

```bash
# From repo root — derives observability.env + librechat/.env from CLICKHOUSE_DSN:
export ANTHROPIC_API_KEY   # optional, for LibreChat only
./clickhouse/scripts/sync_librechat_env.sh

podman-compose --profile observability up -d   # OTLP :4317 / :4318

# Point Pulse at the collector:
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 podman-compose up -d backend frontend redis
```

Equivalent one-liner (manual):

```bash
podman run -d --name pulse-clickstack \
  -e CLICKHOUSE_ENDPOINT="https://YOUR.host.clickhouse.cloud:8443" \
  -e CLICKHOUSE_USER="default" \
  -e CLICKHOUSE_PASSWORD="YOUR_PASSWORD" \
  -p 4317:4317 -p 4318:4318 \
  clickhouse/clickstack-otel-collector:latest
```

## View metrics in ClickHouse Cloud

Traces land in ClickStack-managed tables on your Cloud service. In the SQL
console, try:

```sql
SHOW TABLES LIKE '%otel%';
SELECT * FROM system.tables WHERE name ILIKE '%trace%' OR name ILIKE '%span%';
```

Then explore span duration / service name (`pulse-concurrency-api`) once you've
driven a few chart queries with `OTEL_EXPORTER_OTLP_ENDPOINT` set.

## Local HyperDX (legacy)

`clickstack/docker-compose.yml` still runs the all-in-one HyperDX UI on :8081 for
local-only demos. Prefer the Cloud collector above for hackathon parity with
ClickHouse Cloud.
