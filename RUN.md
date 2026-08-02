# RUN.md — Pulse

Env vars and commands to load data, serve the concurrency curve, and turn on
integrations. Install prerequisites first — see [README.md](./README.md#how-to-run-it).

**Platforms:** bash on **macOS** and **Linux**. On Windows, use **WSL2**.

---

## Fastest path (Cloud DSN already set)

```bash
cp .env.example .env
# edit CLICKHOUSE_DSN → your ClickHouse Cloud native secure URL (:9440?secure=true)

export $(grep -v '^#' .env | xargs)
cd backend

# 1. Schema
go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" -migrations ../clickhouse/migrations -reload-dict

# 2. Load + build (point -in at your CSVs)
go run ./cmd/loadraw        -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN"
go run ./cmd/loadcontent    -in ../hackathon-data/data/ch-hackathon-content-data.csv -dsn "$CLICKHOUSE_DSN"
go run ./cmd/build_segments -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN" -segments= -deltas=
go run ./cmd/build_user_segments -dsn "$CLICKHOUSE_DSN" -config ../clickhouse/scripts/config.env

# 3. Evidence
go run ./cmd/bench -dsn "$CLICKHOUSE_DSN" \
  -spec ../clickhouse/queries/benchmark/spec.example.json -out ../evidence
go run ./cmd/validate -dsn "$CLICKHOUSE_DSN" \
  -in ../hackathon-data/data/ch-hackathon-raw-data.csv

# 4. API + UI
CLICKHOUSE_DSN="$CLICKHOUSE_DSN" PREFLIGHT_ENABLED=false go run ./cmd/server   # :8080
# other shell:
cd ../frontend && npm install && npm run dev   # :5173 → proxies /api → :8080
```

Containers (Cloud DSN in `.env`):

```bash
docker compose up backend frontend redis
```

---

## One-command unseen day

When the sealed evaluation CSVs land:

```bash
./clickhouse/scripts/unseen_day.sh /path/to/raw.csv /path/to/content.csv
```

Emits [`evidence/unseen_day/`](evidence/unseen_day/) (`answers.json`, consistency,
query log, sensitivity, run log).

---

## Environment

```bash
cp .env.example .env
```

| Variable | Default / notes | Source |
|----------|-----------------|--------|
| `CLICKHOUSE_DSN` | Cloud native `:9440/sony_liv?secure=true` | **Required** |
| `ADDR` | `:8080` | API listen |
| `REDIS_ADDR` | `localhost:6379` | Optional preflight |
| `PREFLIGHT_ENABLED` | `true` — set `false` to skip Redis | API |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://127.0.0.1:4318` | ClickStack collector |
| `LANGFUSE_PUBLIC_KEY` / `SECRET_KEY` / `HOST` | Cloud keys | LiteLLM → Langfuse |
| `LITELLM_BASE_URL` | Local/org LiteLLM | LibreChat custom endpoint |
| `VITE_API_TARGET` | `http://localhost:8080` | Frontend proxy |

Secrets stay in gitignored `.env`, `observability.env`, `librechat/librechat.runtime.yaml`.

---

## Observability + chat

```bash
# Derive observability.env + librechat env from CLICKHOUSE_DSN
./clickhouse/scripts/sync_librechat_env.sh

# ClickStack collector (OTLP → ClickHouse Cloud)
docker compose --profile observability up -d clickstack

# API with traces (prefer 127.0.0.1 over localhost on Podman/macOS)
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
  CLICKHOUSE_DSN="$CLICKHOUSE_DSN" PREFLIGHT_ENABLED=false \
  go run ./backend/cmd/server

# LibreChat + pulse MCP + ClickHouse MCP
docker compose --profile chat up -d
```

Useful URLs:

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:5173 |
| API | http://localhost:8080 |
| LibreChat | http://localhost:3080 |
| ClickStack OTLP | http://127.0.0.1:4318 |
| Langfuse Cloud | https://cloud.langfuse.com |

Apply ClickStack SQL views once: paste [`clickstack/dashboards.sql`](clickstack/dashboards.sql)
into the Cloud SQL console.

Smoke:

```bash
./clickhouse/scripts/smoke_integrations.sh
```

---

## CLI cheat sheet

| Command | Purpose |
|---------|---------|
| `go run ./cmd/pipeline …` | Apply migrations (+ optional `-drop`, `-exec`) |
| `go run ./cmd/loadraw …` | CSV → `raw_events` |
| `go run ./cmd/loadcontent …` | Content CSV → `content_metadata` + dict reload |
| `go run ./cmd/build_segments …` | Segments + session minute deltas |
| `go run ./cmd/build_user_segments …` | User-grain segments + deltas |
| `go run ./cmd/bench …` | Benchmark → `evidence/` |
| `go run ./cmd/validate …` | Invariants + sensitivity |
| `./clickhouse/scripts/unseen_day.sh` | Full sealed-day pipeline |
| `./clickhouse/scripts/replay.sh` | Incremental watermark demo |
| `./clickhouse/scripts/start_chat_fresh.sh` | Clean LibreChat stack |

All `go run` commands are from `backend/` unless noted.

---

## Incremental / live demo

```bash
./clickhouse/scripts/replay.sh <raw.csv> <watermark_epoch_ms>
```

Truncates and replays up to a watermark so open sessions and reconcile are visible
in the Replay view.
