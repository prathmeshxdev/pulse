# Pulse — Project Summary

**Pulse** is a Click-a-thon 2026 solution for Sony LIV foreground-only concurrent-viewer analytics on ClickHouse Cloud. The problem asks for peak and average concurrency at minute, hour, and day grain with arbitrary dimension filters—while excluding background playback and paused sessions. Pulse answers with a **defensible semantic model**, a **reproducible serving layer**, and multiple consumers that all read the same modeled tables.

## The core idea

Raw playback events are noisy: users background the app, pause, buffer, or leave sessions open. Pulse defines “actively watching” with four binding conditions (foreground, playback started, not paused, within session bounds). A Go state machine converts raw events into **session active segments**—intervals where all conditions hold. Instead of exploding every segment across every minute, a sweep-line emits narrow **minute deltas** (+1 / −1 per segment boundary). Concurrency at any minute is the cumulative sum of filtered deltas. One primitive drives minute, hour, and day metrics, so grains cannot disagree.

## Architecture (four layers)

**Ingestion.** Batch pipeline over the native protocol: `loadraw` into `raw_events`, `build_segments` into `session_active_segments` and `minute_deltas`. Loads are idempotent via staging tables and atomic `REPLACE PARTITION`—readers never see an empty partition mid-rebuild. Dynamic JSON properties on segments feed a refreshable materialized view (`properties_key_mappings`) that catalogs filterable keys and types.

**Caching.** Redis serves two roles: **preflight** singleflight plus TTL result cache for identical chart queries, and **live state** for streaming demos (`streamd`) with fixed session TTL and O(1) active counts. Both degrade gracefully when Redis is unavailable.

**Serving.** A Go API compiles normative chart queries (semi-join on `segment_id` or `user_segment_id`, never widening delta rows). Pass `"unit": "user"` for session-independent (user-level) peak/average; default `"unit": "session"` is session-aware. A React dashboard and live-replay view consume the API. LibreChat plus the official ClickHouse MCP server provides a conversational layer over read-only serving tables—complementary to the API, not a duplicate compiler.

**Observability.** ClickStack (OTLP collector → ClickHouse Cloud `otel_*`) traces API and pipeline work. Optional Langfuse traces LLM generations when LibreChat routes through a local LiteLLM proxy with callbacks.

## Assumptions and limits

### Lookback period (`MAX_SEGMENT_SPAN_HOURS = 72`)

The query template bounds the segment semi-join (`sel`) and opening balance to **window + 72h lookback**. Two bounds apply:

- **Overlap bound (theorem):** segments outside the query window contribute either a cancelling ±1 pair or nothing, so restricting to overlapping segments is answer-preserving.
- **Lookback bound (asserted precondition):** valid only while no segment exceeds 72 hours. The longest measured session is **43.6h**; `cmd/validate` asserts this on every pipeline run. If violated, straddling segments are dropped and the opening balance can silently shorten—so the pipeline fails loudly rather than returning a wrong number.

At scale this turns O(history) scans into O(window + 72h). The same 72h bound is reused for Redis TTL and the `streamd` lateness window. At 100×, force-splitting segments at UTC day boundaries converts the assumption into a guarantee.

### Semantic parameters (`config.env`)

Each knob is one line in `clickhouse/scripts/config.env`; flipping any requires a segment rebuild. Measured impact (full window, vs baseline) is in `evidence/sensitivity.md`:

| Parameter | Default | Impact if flipped |
|-----------|---------|-------------------|
| Pause counts as active (D2) | No | ~+2% peak/avg |
| Buffering counts as active (D3) | Yes | ~+34% peak, +42% avg — **dominant knob** |
| Heartbeat grace | 90s | Near-inert (0.87% of gaps > 90s) |
| Minute attribution | any-overlap | Highest-risk locked choice vs private ground truth |
| Average denominator | all clock minutes | Larger swing on narrow filters if flipped |
| Timezone | UTC | Day grain at 00:00 UTC (05:30 IST); IST is a query-layer one-liner |
| Dimension snapshot | at segment start (R10) | No mid-session split on language change |
| Open segments | `least(last_hb + grace, watermark)` | Prevents phantom tail past end of known data |

### Open questions we defaulted on

| # | Question | Default until answered |
|---|----------|------------------------|
| Q1 | User-level concurrency? | **Shipped** — `user_active_segments` + `user_minute_deltas`; API/bench `unit=user` |
| Q2 | Hour/day grain definition? | Bucket the same minute curve (peak = max of minutes in bucket) |
| Q3 | Pre-aggregate rollup? | No until p95 > 200 ms — measure, don't guess |
| Q4 | Product depth? | ClickStack + minimal chart; LibreChat secondary |
| Q5 | Split segment on dimension change? | Snapshot at start; 99.96% of sessions vary subtitle mid-flight |

### Known limits

- The training CSV has **zero open sessions** as loaded; the incremental path is demonstrated via `clickhouse/scripts/replay.sh` (watermark split + reconcile).
- LibreChat MCP writes ad-hoc SQL—not the same compiler as `/api/v1/concurrency/chart`—so answers can diverge.
- A Kafka consumer is not shipped; CSV replay + `streamd` prove the streaming design.
- At training scale, latency cannot discriminate between designs; semantics and the sensitivity matrix are the score.

Every parameter flip is explainable: one line in `config.env` plus a measured delta in `evidence/sensitivity.md`.

## What we deliberately did not build

Kafka streaming workers and a separate metadata registry appear on the high-level diagram as future paths; today the backend reads `properties_key_mappings` directly. **pulse MCP** ships for API-parity chat answers; ClickHouse MCP remains for read-only SQL exploration.

## Integrations

ClickHouse Cloud hosts all serving data and ClickStack OTLP tables. LibreChat runs with the ClickHouse MCP server under a read-only user. Langfuse Cloud (or self-host) captures LLM traces when a LiteLLM proxy is configured with success/failure callbacks—org upstream URLs and API keys stay in gitignored `.env` files only.

Pulse is MIT-licensed. See `README.md`, `Architecture.md`, and `RUN.md` for setup, and `presentations/pulse-by-layers/` for the architecture deck.
