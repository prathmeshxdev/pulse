# Pulse — Architecture

## What we’re building

Sony LIV asks: *how many people are watching right now?* — counting only
**foreground-active** playback (not paused, not backgrounded, not heartbeat-missing)
at minute / hour / day grain with arbitrary dimension filters.

**Pulse** answers with one semantic model and one serving primitive, consumed by a
dashboard, a live-replay view, and a LibreChat + MCP conversational layer — all
reading the same ClickHouse Cloud tables.

![Pulse system HLD — four layers](presentations/pulse-by-layers/public/hld.png)

*System HLD: ingestion → caching → serving (API, dashboard, LibreChat) → observability (ClickStack, Langfuse).*

---

## Four layers

| Layer | Role | Key pieces |
|-------|------|------------|
| **01 Ingestion** | Raw CSV → typed serving tables | `loadraw`, `build_segments`, `build_user_segments`, migrations |
| **02 Caching** | Optional preflight + live state | Redis singleflight / TTL; degrades when absent |
| **03 Serving** | Normative concurrency compiler + UI | Go API, React dashboard, LibreChat + pulse MCP |
| **04 Observability** | Traces for API/pipeline + LLM | ClickStack (OTLP → Cloud `otel_*`), Langfuse via LiteLLM |

There is no separate “orchestrator microservice.” Batch pipeline and API share
`backend/internal/{segments,deltas,concurrency,filters,chclient}`.

Full deck: [`presentations/pulse-by-layers/`](presentations/pulse-by-layers/) ·
PDF: [`pulse-by-layers.pdf`](./pulse-by-layers.pdf).

---

## Concurrency model (the curve)

Raw events → Go state machine → **session active segments** (intervals where all
four “actively watching” conditions hold) → sweep-line **minute deltas** (+1/−1).

Concurrency at minute *t* = opening balance + cumulative sum of filtered deltas.

```text
raw_events
    → session_active_segments / user_active_segments
    → minute_deltas / user_minute_deltas
    → POST /api/v1/concurrency/chart  (compiled SQL, never reads raw_events)
```

**Normative query** is compiled in
[`backend/internal/concurrency/query.go`](backend/internal/concurrency/query.go)
(`BuildChartQuery`):

1. Optional `sel` CTE — filter overlapping segments (typed columns, `content_dict`, or `properties`)
2. `open_edges` — still-open sessions contribute live ±1 edges
3. `opening` — sum of deltas before the window (lookback ≤ 72h)
4. `net` — per-minute net deltas inside the window
5. `curve` — dense minute grid + running sum → **concurrency**
6. Metric wrapper — `timeseries` / `peak` / `avg` / `summary` at minute|hour|day

Filters never widen delta rows; they semi-join on `segment_id` / `user_segment_id`.

Semantic constants live in [`clickhouse/scripts/config.env`](clickhouse/scripts/config.env).

---

## Serving surfaces

| Surface | Path | Reads |
|---------|------|-------|
| Dashboard + filters | `frontend/` → `POST /api/v1/concurrency/chart` | Same compiler as bench |
| Breakdown | `POST /api/v1/concurrency/breakdown` | Peak/avg by dimension |
| Live replay | `ReplayView` + `streamd` / `replay.sh` | Watermarked open sessions |
| Chat | LibreChat → **pulse MCP** (`librechat/pulse-mcp`) | Proxies chart/breakdown API |
| Read-only SQL (optional) | ClickHouse MCP | Serving tables only (`pulse_readonly`) |

Rule of thumb: **pulse MCP / API numbers beat ad-hoc SQL**; the system prompt
([`librechat/system_prompt.md`](librechat/system_prompt.md)) steers the agent accordingly.

---

## Filter routing

Every UI/API filter resolves through one path (`filters.ResolveDimension`):

| Kind | Storage | Example |
|------|---------|---------|
| **segment** | typed column on segments / deltas | `platform`, `country` |
| **dict** | `content_dict` via `dictGet` | `show_name`, `video_type`, `category`, `title` |
| **property** | `properties` JSON + type catalog | `video_resolution`, `device_model`, … |

Unknown CSV event fields land in `properties` automatically (migration `010`);
content fields that need dict joins get typed DDL (e.g. `013_show_name.sql`).

Full filter → dataset column map: [README § Dataset filters](README.md#dataset-filters-filter--column-map).

---

## Design choices

### Why pulse MCP (custom MCP)

The official ClickHouse MCP server exposes read-only SQL against serving tables. That
is useful for exploration, but ad-hoc SQL is **not** the same program as the dashboard:
hand-written queries can mis-count concurrency (wrong grain, missing `open_edges`,
filter predicates on widened delta rows, session vs user unit confusion).

**pulse MCP** ([`librechat/pulse-mcp/`](librechat/pulse-mcp/)) is a thin SSE proxy to
`POST /api/v1/concurrency/chart` and `/concurrency/breakdown` — the same Go compiler
the React UI and benchmark runner use. LibreChat agents get peak, average, timeseries,
and breakdown numbers that **match the product** without reimplementing the query template.

ClickHouse MCP remains available for schema inspection; the agent system prompt
([`librechat/system_prompt.md`](librechat/system_prompt.md)) prioritizes pulse MCP for
numeric answers.

### Why JSON `properties` on `session_active_segments`

Typed columns cover stable, high-cardinality event dimensions (`platform`, `country`, …)
and content attributes join through `content_dict`. The unseen evaluation dataset adds
fields such as `video_resolution` that were not in the original DDL.

A **`properties` JSON** column on `session_active_segments` (migration `010`) lets
unknown CSV columns land at ingest **without a migration per new key**. Values are
snapshotted at segment start (R10). A refreshable materialized view
(`properties_key_mappings`) catalogs keys and ClickHouse-inferred types so filters
compile to typed JSON paths — the same `ResolveDimension` path as segment and dict dims.

Content-level fields that must join on `content_id` (e.g. `show_name`) still get typed
DDL and dictionary updates when the schema is known upfront (`013_show_name.sql`).

---

## Observability

| Tool | Role | Wiring |
|------|------|--------|
| **ClickStack** | API + pipeline OTLP traces / metrics / logs → Cloud | `backend/internal/otelx`, compose `clickstack`, [`clickstack/`](clickstack/) |
| **Langfuse** | LLM generations + tool calls for chat | LiteLLM `success_callback: ["langfuse"]`, [`langfuse/`](langfuse/) |
| **LibreChat** | Conversational UI + agents + MCP | [`librechat/`](librechat/), `docker-compose.yml` profile `chat` |

ClickStack writes to ClickHouse Cloud `default.otel_*`. Dashboard SQL:
[`clickstack/dashboards.sql`](clickstack/dashboards.sql).

---

## Unseen-day / evidence

Sealed evaluation data is loaded via
[`clickhouse/scripts/unseen_day.sh`](clickhouse/scripts/unseen_day.sh) → truncates
serving tables, loads, builds segments/deltas/user grain, validates, benches →
[`evidence/unseen_day/`](evidence/unseen_day/).

Trust artifacts: invariants, delta-vs-explosion cross-check, sensitivity matrix
(`cmd/validate`), consistency checks (`concurrency/verify.go`).

---

## Deliberately not built

Kafka workers, a second MCP that reimplements the query compiler, and a separate
metadata registry service. Scaling argument for 100× is analytical (see deck),
not a stopwatch on a cache-fitting training set.
