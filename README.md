# Pulse

## Track

SonyLIV

## Project

**Pulse** — Foreground-only concurrent-viewer analytics on ClickHouse: a defensible
semantic model, minute-grain curve with filters, live replay, and a LibreChat + MCP
chat layer — fully observed with ClickStack and Langfuse.

## Team Members

- Prathmesh ([prathmeshxdev](https://github.com/prathmeshxdev))
- Mohit Agarwal (mohit.agarwal@zepto.com)

## What it does

“How many people are watching right now?” is harder than it looks: an open app is
not a watching viewer. Pulse counts only **foreground-active** playback — excluding
paused, backgrounded, and heartbeat-missing periods — and answers minute / hour /
day queries with arbitrary dimension filters.

1. **Ingest + model** — CSV → typed `raw_events` → session (and user) active segments
   + sweep-line minute deltas on ClickHouse Cloud
2. **Serve the curve** — Go query compiler (`BuildChartQuery`) + React dashboard with
   filters, breakdown, and live replay
3. **Conversational layer** — LibreChat agents call **pulse MCP** (API-backed numbers),
   optional read-only ClickHouse MCP for exploration
4. **Observability** — ClickStack OTLP (API/pipeline → Cloud `otel_*`); Langfuse traces
   LLM + tool calls via LiteLLM

Design position: at training scale every team’s queries are fast. Pulse competes on
**defensible semantics and reproducible correctness** — what “actively watching”
means, why pause ≠ buffer, and evidence that sealed-day answers came from the
pipeline. Scaling to 100× is argued analytically in the architecture deck.

All dataset content is **synthetic**. No real customer data or PII.

## Hosted Demo

<!-- TODO: replace after deploy -->

**[Live demo](YOUR_HOSTED_DEMO_URL)** — dashboard with concurrency curve, filters,
breakdown, and (if deployed) LibreChat.

The demo covers:

- Full-window concurrency curve with visible peaks / ramps
- Dataset filters applied live to the curve (platform, geo, content, properties, …)
- Breakdown by dimension (e.g. platform / show_name)
- Optional: LibreChat asking peak/avg via pulse MCP
- Optional: ClickStack / Langfuse evidence in the walkthrough video

## Demo Video

<!-- TODO: replace with your 2–3 min recording -->

**[Demo video (2–3 min)](YOUR_DEMO_VIDEO_URL)**

Should show the concurrency curve + filters working live, plus a short walkthrough
of ClickStack dashboards and a Langfuse trace / LibreChat turn if claimed.

## Architecture

See [`Architecture.md`](./Architecture.md) for the 1–2 pager.

Slide deck (four layers, HLD, assumptions, scaling):
[`presentations/pulse-by-layers/`](presentations/pulse-by-layers/).

Deeper narrative: [`summary.md`](./summary.md).

### Pitch deck PDF

Export the Slidev deck for submission:

```bash
cd presentations/pulse-by-layers
npm install
npx slidev export --format pdf   # → pitch-deck.pdf (or copy to repo root)
```

Place `pitch-deck.pdf` in the submissions folder when opening the PR.

## How to run it

**Full setup + commands:** see [`RUN.md`](./RUN.md).

### Supported platforms

| Platform | Local run | Notes |
|----------|-----------|-------|
| **macOS** | Supported | Primary path |
| **Linux** | Supported | Same bash + Docker/Podman flow |
| **Windows (WSL2)** | Supported | Run inside WSL + Docker Desktop |
| **Windows (native)** | **Not supported** | Use WSL2 |

### Prerequisites

| Tool | Why | Install |
|------|-----|---------|
| **Go 1.22+** | API, pipeline, bench | [go.dev](https://go.dev/dl/) |
| **Node 20+** | Frontend (+ LibreChat pulse MCP) | [nodejs.org](https://nodejs.org/) |
| **ClickHouse Cloud** (or local) | Primary datastore | Event credits / compose `--profile local` |
| **Docker / Podman** (optional) | Compose stack, ClickStack, LibreChat | Docker Desktop / Podman |
| **Redis** (optional) | Preflight cache — disable with `PREFLIGHT_ENABLED=false` | compose `redis` |

```bash
cp .env.example .env   # set CLICKHOUSE_DSN
# then follow RUN.md
```

---

## Dataset filters (filter → column map)

SonyLIV guideline: document which dataset columns back each filter. Filters apply
to the concurrency curve and breakdown via `POST /api/v1/concurrency/chart` /
`/breakdown` (same `filters` array).

| UI / API dimension | Dataset source | Storage kind | Notes |
|--------------------|----------------|--------------|-------|
| `platform` | raw event `platform` | segment column | e.g. `ANDROID_PHONE` |
| `country` | raw event `country` | segment column | |
| `content_id` | raw event `content_id` | segment column | numeric |
| `app_version` | raw event `app_version` | segment column | |
| `audio_language` | raw event `audio_language` | segment column | |
| `subtitle_language` | raw event `subtitle_language` | segment column | |
| `player_version` | raw event `player_version` | segment column | |
| `user_id` | raw event `user_id` | segment column | |
| `title` | content `title` | `content_dict` | via `dictGet` |
| `video_type` | content `video_type` | `content_dict` | |
| `category` | content `category` | `content_dict` | |
| `show_name` | content `show_name` | `content_dict` | unseen-day field; migration `013` |
| `video_resolution` | raw event `video_resolution` | `properties` JSON | typed via `properties_key_mappings` |
| other unknown event cols | remaining CSV columns | `properties` JSON | auto-cataloged; no DDL |

Routing code: [`backend/internal/filters/filters.go`](backend/internal/filters/filters.go).
Schema discovery: `GET /api/v1/schema/dimensions`, `GET /api/v1/schema/values?dimension=…`.

---

## Concurrency curve (query)

The product UI plots the minute (or hour/day) curve from
`POST /api/v1/concurrency/chart` with `"metric":"timeseries"`.

Compiled SQL shape (see [`backend/internal/concurrency/query.go`](backend/internal/concurrency/query.go)):

```sql
-- Conceptual; real SQL is built by BuildChartQuery
WITH
  sel AS ( … filtered segment ids … ),          -- omitted if no filters
  open_edges AS ( … ±1 for still-open sessions … ),
  opening AS ( SELECT sum(delta) … before window … ),
  net AS ( SELECT minute, sum(delta) … in window … ),
  grid AS ( SELECT … every minute in [start, end) … ),
  curve AS (
    SELECT g.minute,
           opening.c0 + sum(net) OVER (ORDER BY g.minute) AS concurrency
    FROM grid g LEFT JOIN net …
  )
SELECT minute, concurrency FROM curve ORDER BY minute;
```

Peak = `max(concurrency)`; average = `avg(concurrency)` over **all clock minutes**
in the window (including zeros). Unit `"session"` (default) or `"user"`.

---

## Integrations evidence

Wiring is committed (compose, `.env.example`, SDK code). Hosted demo + video should
show these live; screenshots alone are not enough per submissions guidelines.

### ClickStack

- Collector: `docker-compose.yml` profile `observability` (`clickstack` service)
- SDK: [`backend/internal/otelx/otelx.go`](backend/internal/otelx/otelx.go) — traces, metrics, logs
- Instrumented: chart/breakdown handlers, HTTP middleware, pipeline commands
- Destination: ClickHouse Cloud `default.otel_traces` / `otel_logs` / `otel_metrics_*`
- Dashboards: [`clickstack/dashboards.sql`](clickstack/dashboards.sql) · guide [`clickstack/README.md`](clickstack/README.md)

<!-- TODO: add screenshots under docs/evidence/clickstack/ and link here -->

### Langfuse

- Path: LibreChat → LiteLLM (`success_callback: ["langfuse"]`) → models
- Guide: [`langfuse/README.md`](langfuse/README.md)
- Env (redacted): `LANGFUSE_PUBLIC_KEY` / `SECRET_KEY` / `HOST` in `.env.example`

<!-- TODO: paste public share links or commit exported JSON under evidence/langfuse/ -->

**Public share links / exports (graded runs):**

| Run | Link or file |
|-----|----------------|
| _TBD_ | _public Langfuse URL or `evidence/langfuse/*.json`_ |

### LibreChat

- Config: [`librechat/librechat.yaml`](librechat/librechat.yaml) (runtime overlay gitignored)
- Pulse MCP: [`librechat/pulse-mcp/`](librechat/pulse-mcp/) — tools wrap chart/breakdown API
- System prompt: [`librechat/system_prompt.md`](librechat/system_prompt.md)
- Setup: [`librechat/AGENT_SETUP.md`](librechat/AGENT_SETUP.md)

<!-- TODO: judge test credentials if chat is part of hosted demo -->

---

## Unseen-day evidence

Pipeline output for the sealed evaluation dataset:

| Artifact | Path |
|----------|------|
| Answers | [`evidence/unseen_day/answers.json`](evidence/unseen_day/answers.json) |
| Consistency | [`evidence/unseen_day/consistency.json`](evidence/unseen_day/consistency.json) |
| Invariants / sensitivity | [`evidence/unseen_day/`](evidence/unseen_day/) |
| Query log / parts | `query_log.json`, `parts.json` |

One command: `./clickhouse/scripts/unseen_day.sh <raw.csv> <content.csv>` (see [`RUN.md`](./RUN.md)).

---

## For a judge, in five minutes

| Question | Where |
|----------|-------|
| What counts as “actively watching”? | Deck + `clickhouse/scripts/config.env` |
| Why pause out / buffer in? | `evidence/sensitivity.md` |
| Peak / average definition? | One minute curve; max / mean over clock minutes |
| Why trust without an answer key? | `cmd/validate` → `evidence/` |
| Does it scale? | Deck “Scaling to 100×” |
| Filter → column? | [table above](#dataset-filters-filter--column-map) |

## Problem source

- [SonyLIV problem statement](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/PROBLEM_STATEMENT.md)
- [Dataset details](https://github.com/sidagarwal04/click-a-thon-2026/blob/main/SonyLiv/dataset_details.md)
- [Submission guidelines](https://github.com/sidagarwal04/click-a-thon-26-submissions/blob/main/SONYLIV_SUBMISSION_GUIDELINES.md)

## Repo layout

```
pulse/
├── README.md                     # this file (submission front door)
├── Architecture.md               # 1–2 pager
├── RUN.md                        # setup + one-command paths
├── summary.md                    # deeper design narrative
├── presentations/pulse-by-layers/# Slidev deck → export pitch-deck.pdf
├── clickhouse/                   # migrations, scripts, benchmark spec
├── backend/                      # Go API + pipeline
├── frontend/                     # React dashboard + replay
├── librechat/                    # LibreChat + pulse MCP
├── clickstack/                   # OTEL / ClickStack
├── langfuse/                     # Langfuse guide
└── evidence/                     # bench + unseen_day artifacts
```

## License

MIT — see [`LICENSE`](./LICENSE).
