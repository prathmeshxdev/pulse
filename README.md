# Pulse — Foreground-Only Concurrency (Sony LIV)

Click-a-thon 2026 solution: foreground-only concurrent-viewer analytics on ClickHouse, at minute, hour, and day grain, with arbitrary dimension filters. **Pulse** is the serving layer (typed DDL + narrow sweep-line deltas), a Go segment builder / query compiler / chart API, a React dashboard with a live-replay view, and a LibreChat + ClickHouse-MCP conversational layer.

**The design position, stated up front.** At ~10,866 sessions and ~10⁵ delta rows, every team's queries will be fast, so measured latency cannot be the differentiator. This solution competes on **defensible semantics and reproducible correctness**: what "actively watching" means, why paused playback is excluded while buffering is not, and why each benchmark answer is the one a viewer of the concurrency curve would read off it. The scaling argument is made analytically for 100× rather than by benchmarking a dataset that fits in cache.

## Quickstart — run it

Prereqs: Go 1.22+, Node 20+, and a ClickHouse (Cloud service or local). `cp .env.example .env` and set `CLICKHOUSE_DSN` (Cloud native secure port is 9440, `?secure=true`).

```bash
export CLICKHOUSE_DSN='clickhouse://default:PASS@your-instance.clickhouse.cloud:9440/sony_liv?secure=true'
cd backend

# 1. Create database, tables, dictionary
go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" -migrations ../clickhouse/migrations -reload-dict

# 2. Load raw events + content, then build segments + deltas (idempotent)
go run ./cmd/loadraw        -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN"
go run ./cmd/build_segments -in ../hackathon-data/data/ch-hackathon-raw-data.csv -dsn "$CLICKHOUSE_DSN" -segments= -deltas=
#   (content: loadraw only handles raw_events; load content via clickhouse-client
#    or the migration+INSERT in clickhouse/scripts/load_data.sh, then -reload-dict)

# 3. Benchmark set → answers.json + latency + query_log/parts evidence
go run ./cmd/bench -dsn "$CLICKHOUSE_DSN" -spec ../clickhouse/queries/benchmark/spec.example.json -out ../evidence

# 4. API (add PREFLIGHT_ENABLED=false to skip Redis)
CLICKHOUSE_DSN="$CLICKHOUSE_DSN" PREFLIGHT_ENABLED=false go run ./cmd/server
```

Frontend (separate shell):

```bash
cd frontend && npm install && npm run dev   # http://localhost:5173 (proxies /api → :8080)
```

Or everything in containers (Cloud DSN in `.env`): `docker compose up backend frontend redis`. A local ClickHouse for dev: `docker compose --profile local up`.

**Observability + chat (optional):**

```bash
clickhouse/scripts/setup_integrations.sh          # ClickStack :8081 + LibreChat :3080 + MCP :8001
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 docker compose up backend frontend redis
clickhouse/scripts/smoke_integrations.sh          # verify API, CH, OTLP, MCP
```

See [`clickstack/`](clickstack/) and [`librechat/`](librechat/) for details.

Incremental / open-session demo (truncate-and-replay): `clickhouse/scripts/replay.sh <raw.csv> <watermark_epoch_ms>`.

The pipeline is pure Go over the native protocol, so steps 1–4 work against ClickHouse Cloud with no `clickhouse-client` install.

## Start here

| Resource | Role |
|----------|------|
| [`presentations/pulse-by-layers/`](presentations/pulse-by-layers/) | Architecture deck — four layers, HLD, assumptions, scaling, future scope |
| [`librechat/system_prompt.md`](librechat/system_prompt.md) | Serving tables + semantics for the MCP agent |
| [`clickhouse/scripts/config.env`](clickhouse/scripts/config.env) | Frozen semantic constants (pause, buffering, lookback, attribution) |
| [`evidence/`](evidence/) | Benchmark answers, sensitivity matrix, validation artifacts |
| [`clickstack/`](clickstack/) · [`langfuse/`](langfuse/) | Observability integration guides |

## For a judge, in five minutes

| Question | Where it is answered |
|---|---|
| What counts as "actively watching"? | Four conditions: foreground, playback started, not paused, session open — see presentation deck + `config.env` |
| Why is paused playback excluded but buffering included? | Deliberate viewer-intent asymmetry; sensitivity in `evidence/sensitivity.md` |
| How are peak and average defined, exactly? | One primitive: filtered minute curve; peak = max, average = mean over all clock minutes |
| Why is the answer trustworthy without an answer key? | Hand-computed fixtures, automated invariants, delta-vs-explosion cross-check, published sensitivity matrix — `cmd/validate` → `evidence/` |
| Does it scale, and where does it break? | Presentation deck “Scaling to 100×” + “Assumptions & limits” slides |
| What is deliberately not built? | Presentation deck “Future scope” slide |

## Problem source

- [SonyLIV problem statement](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/PROBLEM_STATEMENT.md)
- [Dataset details](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/dataset_details.md)

## Repo layout

```
pulse/
├── presentations/pulse-by-layers/  # Slidev architecture deck + HLD image
├── clickhouse/
│   ├── migrations/               # typed DDL (no JSON columns)
│   ├── queries/                  # benchmark spec + reconcile_session.sql
│   └── scripts/                  # config.env, replay.sh, unseen_day.sh, create_readonly_user.sql
├── backend/                      # Go module (github.com/prathmeshxdev/pulse)
│   ├── cmd/server                # POST /api/v1/concurrency/chart, /schema/*
│   ├── cmd/loadraw               # CSV → raw_events (native, Cloud-ready)
│   ├── cmd/loadcontent           # content CSV → content_metadata + dict reload
│   ├── cmd/build_segments        # state machine → segments + deltas
│   ├── cmd/reconcile             # incremental correction (published-edge)
│   ├── cmd/validate              # invariants + delta/explosion cross-check + sensitivity
│   ├── cmd/bench                 # benchmark runner → answers.json + evidence
│   ├── cmd/pipeline              # apply migrations
│   └── internal/                 # segments, deltas, concurrency, filters, otelx, …
├── frontend/                     # React + Vite dashboard + live replay
├── clickstack/                   # ClickStack/HyperDX observability (OTel spans)
├── librechat/                    # LibreChat + ClickHouse MCP (conversational)
└── evidence/                     # generated: answers, invariants, sensitivity, query_log
```

Per the problem statement a minimal visualization is sufficient; the dashboard, replay view, and chat layer are built here as the "great looks like" surface, all reading the same serving layer.

**One-command unseen day:** `clickhouse/scripts/unseen_day.sh <raw.csv> <content.csv>` runs the whole pipeline and emits the evidence bundle. **Validation:** `go run ./cmd/validate -dsn … -in <raw.csv>` runs the invariants, the delta-vs-explosion cross-check, and the sensitivity matrix.
