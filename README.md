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

Incremental / open-session demo (truncate-and-replay): `clickhouse/scripts/replay.sh <raw.csv> <watermark_epoch_ms>`.

Conversational layer: see [`librechat/`](librechat/) (`docker compose up -d`, then attach the ClickHouse MCP tool to an agent).

The pipeline is pure Go over the native protocol, so steps 1–4 work against ClickHouse Cloud with no `clickhouse-client` install.

## Start here

Read in this order. Two documents are authoritative and the rest are supporting detail.

| # | Doc | Role | Read it when |
|---|-----|------|--------------|
| 1 | **[docs/FINAL_PLAN.md](docs/FINAL_PLAN.md)** | **Authoritative.** §1 is the complete locked rule set (R1–R10); §16 is the single list of open questions | Always. This is the build plan and the tie-breaker |
| 2 | **[docs/SEMANTICS_SPEC.md](docs/SEMANTICS_SPEC.md)** | **Decision record.** The four binding decisions with rationale, plus what changed from earlier drafts and why | You want to know *why* a rule is what it is, or you are tempted to change one |
| 3 | [docs/DATA_AND_PROBLEM_UNDERSTANDING.md](docs/DATA_AND_PROBLEM_UNDERSTANDING.md) | The problem, the datasets, and the measured data profile | You are new to the problem or need the event taxonomy |
| 4 | [docs/ACTIVE_INTERVAL_LOGIC.md](docs/ACTIVE_INTERVAL_LOGIC.md) | How `session_active_segments` is derived from raw events | You are implementing or debugging the segment builder |
| 5 | [docs/SCHEMA_AND_DDL.md](docs/SCHEMA_AND_DDL.md) | Table DDL, dictionary, idempotency mechanisms, schema evolution, migration map | You are writing migrations or reasoning about physical design |
| 6 | [docs/VALIDATION.md](docs/VALIDATION.md) | Validation layers, invariants, sensitivity matrix, evidence artifacts | You are proving the pipeline correct |
| 7 | [docs/IMPLEMENTATION_PLAN.md](docs/IMPLEMENTATION_PLAN.md) | Phasing, product-layer scope, integration surface | You are planning work or sequencing |

### Which document wins

`FINAL_PLAN.md` §1 is the rule set to implement against. `SEMANTICS_SPEC.md` records the same decisions with fuller rationale and carries the history of what was tried and rejected. **They must never disagree**, and `SEMANTICS_SPEC.md` §0 holds the mapping between them. If any other document contradicts either, the other document is the bug.

## For a judge, in five minutes

| Question | Where it is answered |
|---|---|
| What counts as "actively watching"? | [FINAL_PLAN.md §1.4](docs/FINAL_PLAN.md#14-the-active-predicate) — four conditions, all required |
| Why is paused playback excluded but buffering included? | [FINAL_PLAN.md R1 and R2](docs/FINAL_PLAN.md#15-the-rules-with-rationale). The asymmetry is deliberate and argued from viewer intent |
| How are peak and average defined, exactly? | [FINAL_PLAN.md §2](docs/FINAL_PLAN.md#2-metric-definitions). One primitive: the filtered minute curve |
| Why is the answer trustworthy without an answer key? | Hand-computed fixtures, automated invariants, a delta-vs-minute-explosion arithmetic cross-check, and a published sensitivity matrix — all runnable via `cmd/validate` (see `evidence/`) |
| Does it scale, and where does it break? | [FINAL_PLAN.md §15](docs/FINAL_PLAN.md#15-scaling-to-100), including §15.10 on genuine weaknesses as distinct from managed trade-offs |
| What is deliberately not built? | [FINAL_PLAN.md §12](docs/FINAL_PLAN.md#12-what-we-are-deliberately-not-building) |

## Problem source

- [SonyLIV problem statement](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/PROBLEM_STATEMENT.md)
- [Dataset details](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/dataset_details.md)

## Repo layout

```
pulse/
├── docs/                         # FINAL_PLAN, SEMANTICS_SPEC, DDL, ARCHITECTURE, validation
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
