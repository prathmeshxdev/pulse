---
theme: default
title: Pulse
info: |
  Pulse — Click-a-thon 2026 · Sony LIV foreground concurrency
class: text-center
highlighter: shiki
lineNumbers: false
drawings:
  persist: false
transition: fade
mdc: true
colorSchema: dark
fonts:
  sans: 'Inter'
  serif: 'Inter'
  mono: 'JetBrains Mono'
canvasWidth: 1080
---

<style>
@import './styles/index.css';
:root {
  --rm-bg: #0B1020; --rm-surface: #141B2E; --rm-text: #F5E9D0;
  --rm-gold: #FFD166; --rm-coral: #F4845F; --rm-green: #06D6A0;
  --rm-muted: #8B95B8; --rm-lavender: #B388FF; --rm-border: #2D3656;
}
.slidev-layout {
  background: var(--rm-bg); color: var(--rm-text);
  font-family: 'Inter', -apple-system, sans-serif;
  padding: 3rem 3.5rem !important;
}
.slidev-layout strong { color: var(--rm-coral); }
.bg-glow { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 0; }
.bg-glow::before {
  content: ''; position: absolute; top: -20%; right: -10%;
  width: 55vw; height: 55vw;
  background: radial-gradient(circle, rgba(244,132,95,0.12) 0%, transparent 60%);
  filter: blur(40px);
}
.title-stack {
  position: relative; z-index: 1; height: 100%;
  display: flex; flex-direction: column; justify-content: center; align-items: flex-start; text-align: left;
}
.title-eyebrow {
  font-family: 'JetBrains Mono', monospace; font-size: 0.85rem;
  letter-spacing: 0.35em; color: var(--rm-lavender); text-transform: uppercase; margin-bottom: 1.5rem;
}
.title-main {
  font-size: 6.5rem; font-weight: 800; letter-spacing: -0.04em; line-height: 0.95; margin: 0;
  background: linear-gradient(135deg, #FFD166 0%, #06D6A0 100%);
  -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent;
}
.title-sub { font-size: 1.45rem; color: var(--rm-text); margin-top: 1rem; max-width: 44ch; line-height: 1.35; }
.title-rule { width: 4rem; height: 2px; background: var(--rm-coral); margin: 1.75rem 0; }
.title-byline { font-family: 'JetBrains Mono', monospace; font-size: 0.88rem; color: var(--rm-muted); line-height: 1.5; }
</style>

<div class="bg-glow"></div>

<div class="title-stack">

<div class="title-eyebrow">Click-a-thon 2026 · Sony LIV · ClickHouse Cloud</div>

<h1 class="title-main">Pulse</h1>

<div class="title-sub">
Foreground-only concurrent viewers.
</div>

<div class="title-rule"></div>

<div class="title-byline">
Orbitrons · Click-a-thon 2026 · ClickHouse Cloud<br/>
Dashboard · API · LibreChat · ClickStack · Langfuse
</div>

</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">The problem</h2>
  <div class="pl-sub">Sony LIV · what “concurrent viewers” must mean</div>
  <div class="pl-body">
    <div class="pl-stat-row">
      <div class="pl-stat"><div class="val">905K</div><div class="lbl">raw events</div></div>
      <div class="pl-stat"><div class="val">10.9K</div><div class="lbl">sessions</div></div>
      <div class="pl-stat"><div class="val">~10⁵</div><div class="lbl">delta rows</div></div>
      <div class="pl-stat"><div class="val">20.9K</div><div class="lbl">pause windows</div></div>
    </div>
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Ask</h3>
        <ul>
          <li>Peak &amp; average <strong>foreground-active</strong> concurrency</li>
          <li>Minute, hour, day grain — same filters on all three</li>
          <li>Filter by platform, country, content, app version, languages, player version</li>
          <li>Dynamic JSON <code>properties.*</code> dimensions from CSV extras</li>
          <li>Incremental / open-session updates without full rebuild</li>
          <li>Sub Seconds latencies for dashboards</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Foreground-State Logic</h3>
        <ul>
          <li>App in foreground — not backgrounded</li>
          <li>Playback started — buffering counts; pause does <em>not</em></li>
          <li>Session open — between start and end events</li>
          <li>Heartbeat liveness — stale sessions drop off</li>
        </ul>
        <div class="pl-callout">
          Pause markers live inside <code>VideoHeartbeat</code> via the <code>event</code> column — a classifier on <code>event_type</code> alone is blind to pause.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="hld-full">
  <div class="hld-full-head">
    <h2>System HLD</h2>
    <p>Four layers bottom → top · solid = built · dotted = direct</p>
  </div>
  <div class="hld-full-body">
    <img src="/hld.png" alt="Pulse four-layer architecture" />
  </div>
  <div class="hld-full-foot">
    <div class="hld-layer-pill">01 Ingestion<span>Kafka → workers → CH</span></div>
    <div class="hld-layer-pill">02 Caching<span>preflight + live state</span></div>
    <div class="hld-layer-pill">03 Serving<span>API · UI · LibreChat</span></div>
    <div class="hld-layer-pill">04 Observability<span>ClickStack · Langfuse</span></div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 01 · Real-time ingestion</h2>
  <div class="pl-sub">Kafka workers · Redis hot state · ClickHouse serving layer</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Live path</h3>
        <ul>
          <li>Client playback events → <strong>Kafka</strong> → ingestion workers</li>
          <li>Per session: foreground-active state machine in <strong>Redis</strong> (O(1) per event)</li>
          <li>Segment <strong>closes</strong> → compacted <code>±1</code> edges land in <code>minute_deltas</code></li>
          <li>Segment <strong>still open</strong> → live row in <code>session_active_segments</code> (corrected at query time)</li>
          <li>Sub-second freshness — curve updates as viewers start/stop watching</li>
          <li>Same logic for CSV replay in the demo; prod swaps in a Kafka consumer</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">sub-second</span>
          <span class="pl-chip">horizontally scalable workers</span>
        </div>
      </div>
      <div class="pl-panel">
        <h3>State machine → sweep-line deltas</h3>
        <ul>
          <li>Four rules: foreground · playing · not paused · session open</li>
          <li>Each active interval → one <code>+1</code> / <code>−1</code> pair (not per-minute explosion)</li>
          <li>Dimensions live on segments, not deltas. Hence new properties doesn't affect this table</li>
          <li>Dynamic JSON <code>properties</code> on segments — new Kafka fields without DDL churn</li>
        </ul>
        <div class="pl-callout">
          Redis holds <strong>hot session state</strong> for workers. ClickHouse is the durable store and what every consumer queries.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 01 · What lands in ClickHouse</h2>
  <div class="pl-sub">Serving tables the query layer reads</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Derived tables (query path)</h3>
        <ul>
          <li><code>session_active_segments</code> — typed dimensions + JSON properties per active interval</li>
          <li><code>minute_deltas</code> — narrow <code>(minute, segment_id, ±1)</code> for the concurrency curve</li>
          <li><code>content_dict</code> — title, video_type, category lookups</li>
          <li><code>properties_key_mappings</code> — refreshable MV catalog for dynamic filter keys</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Why two physical shapes</h3>
        <ul>
          <li><strong>Closed history</strong> → compacted into <code>minute_deltas</code> (SummingMergeTree merges edges)</li>
          <li><strong>Open sessions</strong> → live rows in <code>session_active_segments</code> until the interval ends</li>
          <li>Workers only emit delta pairs on <em>close</em> — avoids double-counting partial intervals</li>
          <li><code>session_live_state</code> MV — optional argMax gauge for “active now” from the event stream</li>
        </ul>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 01 · Compacted + live correction</h2>
  <div class="pl-sub">Two states on one curve — no waiting for sessions to close</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>① Compacted state (historical)</h3>
        <ul>
          <li><code>minute_deltas</code> holds merged <code>±1</code> edges for <strong>finished</strong> active intervals</li>
          <li>SummingMergeTree background merge collapses duplicate keys — cheap scans at query time</li>
          <li>Opening balance + cumulative sum → minute concurrency curve</li>
          <li>This is the durable, compacted path for all closed viewing time</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">SummingMergeTree</span>
        </div>
      </div>
      <div class="pl-panel">
        <h3>② Live correction (incomplete sessions)</h3>
        <ul>
          <li>Still-watching sessions stay in <code>session_active_segments</code> with <code>close_reason = ''</code></li>
          <li>Not yet in <code>minute_deltas</code> — would miss “active now” if we stopped there</li>
          <li>Chart query <strong>open_edges</strong> CTE: synthesize matching <code>+1/−1</code> from live rows</li>
          <li><code>UNION ALL</code> with compacted deltas → same cumulative curve, filters apply to both</li>
        </ul>
        <div class="pl-callout">
          One query template merges both states — compacted history plus live open sessions as a correction layer.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 02 · Caching</h2>
  <div class="pl-sub">Redis — preflight dedupe + live session state</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Preflight — chart query cache</h3>
        <ul>
          <li>Dashboard + bench hammer identical chart queries → singleflight dedupe</li>
          <li>Cache key = SHA256 query fingerprint; <code>SetNX</code> inflight lock</li>
          <li>Result JSON under <code>sony:ch:result:*</code>; TTL configurable (default 1m)</li>
          <li>Redis down → graceful degrade to direct ClickHouse</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">singleflight</span>
          <span class="pl-chip">hot dashboard</span>
        </div>
      </div>
      <div class="pl-panel">
        <h3>Live state — streaming hot path</h3>
        <ul>
          <li><code>streamd</code> → per-session <code>Accumulator</code> in Redis (<code>pulse:session:{id}</code>)</li>
          <li>Fixed TTL 72h from first seen; <code>pulse:active</code> set for O(1) count</li>
          <li>Same state machine as batch — <code>TestStreamingMatchesBatch</code></li>
          <li><code>/live</code> API + <code>open_edges</code> fold open sessions into the curve</li>
        </ul>
        <div class="pl-callout">
          Two Redis roles, different semantics — preflight caches query <em>results</em>; livestate holds ingestion <em>state</em>.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 03 · Query compiler</h2>
  <div class="pl-sub">POST /api/v1/concurrency/chart · one template, all grains</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Normative query shape</h3>
        <ul>
          <li>Resolve filters → <code>segment_id IN (…)</code> semi-join</li>
          <li>Opening balance before window start</li>
          <li>Dense minute grid + cumulative <code>sum(delta)</code></li>
          <li><strong>open_edges</strong> — union live open sessions with compacted <code>minute_deltas</code></li>
          <li>Bucket to hour/day from same minute series</li>
          <li><code>dictGet(content_dict, …)</code> for title / video_type</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>API surface</h3>
        <ul>
          <li><code>/api/v1/concurrency/chart</code> — peak, avg, timeseries, breakdown</li>
          <li><code>/api/v1/concurrency/live</code> — Redis open sessions + CH curve</li>
          <li><code>/api/v1/schema/*</code> — static + dynamic dimensions from MV</li>
          <li>React dashboard + live replay view</li>
          <li>Preflight wraps chart path; OTEL span per request</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">peak</span>
          <span class="pl-chip">avg</span>
          <span class="pl-chip">breakdown</span>
          <span class="pl-chip">properties.*</span>
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 03 · Dashboard &amp; demo</h2>
  <div class="pl-sub">Minimal UI — model and serving layer are the score, not polish</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>React dashboard</h3>
        <ul>
          <li>Peak / average cards + minute timeseries (Recharts)</li>
          <li>Grain toggle: minute · hour · day — same API contract</li>
          <li>Dimension filters + dynamic <code>properties.*</code> from schema API</li>
          <li>Breakdown table — top-N by content, platform, country</li>
          <li>Preflight cache → sub-second repeat queries on hot windows</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Live replay &amp; incremental proof</h3>
        <ul>
          <li><strong>Replay view</strong> — animates the minute curve point-by-point for demo UX</li>
          <li><strong>streamd</strong> — replays CSV as a live stream → Redis → CH on segment close</li>
          <li><code>clickhouse/scripts/replay.sh</code> — watermark split, reconcile tail, before/after bench</li>
          <li><code>/api/v1/concurrency/live</code> — Redis gauge + CH curve union</li>
          <li>Judges see freshness without waiting for full rebuild</li>
        </ul>
        <div class="pl-callout">
          Problem statement: polished frontends are out of scope — we ship enough UI to exercise the serving layer honestly.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 03 · LibreChat + MCP</h2>
  <div class="pl-sub">Second consumer of the serving layer (dotted path on HLD)</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>What we built</h3>
        <ul>
          <li>LibreChat + official <strong>ClickHouse MCP</strong> (SSE)</li>
          <li>Read-only user <code>pulse_readonly</code> — no <code>raw_events</code>, no writes</li>
          <li><code>system_prompt.md</code> — serving tables, semantics, example SQL</li>
          <li>Compose profile <code>chat</code> — MongoDB + MCP + LiteLLM endpoint</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Honest limits</h3>
        <ul>
          <li><strong>pulse MCP</strong> proxies the chart/breakdown API — same compiler as the dashboard</li>
          <li>Optional ClickHouse MCP for schema inspection only — not for concurrency numbers</li>
          <li>Metadata for dynamic keys: <code>schema_dimensions</code> tool or <code>properties_key_mappings</code></li>
        </ul>
        <div class="pl-callout">
          Example: <em>“Peak concurrency on ANDROID_PHONE between 13:00–14:00 UTC?”</em> → <code>pulse</code> MCP → <code>/api/v1/concurrency/chart</code>.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 04 · Observability</h2>
  <div class="pl-sub">ClickStack (API / pipeline) · Langfuse (LLM traces)</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>ClickStack — OTLP → Cloud</h3>
        <ul>
          <li><code>clickstack-otel-collector</code> in docker-compose → <code>otel_*</code> tables</li>
          <li>Backend: <code>OTEL_EXPORTER_OTLP_ENDPOINT=http://clickstack:4318</code></li>
          <li>Spans: <code>concurrency.chart</code>, pipeline CLIs, service <code>pulse-concurrency-api</code></li>
          <li><code>smoke_integrations.sh</code> — API, CH, OTLP, MCP health</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">OpenTelemetry</span>
          <span class="pl-chip">internal/otelx</span>
        </div>
      </div>
      <div class="pl-panel">
        <h3>Langfuse — conversational plane</h3>
        <ul>
          <li>LibreChat → LiteLLM proxy → upstream model</li>
          <li><code>success_callback: ["langfuse"]</code> — prompts, tokens, MCP tool calls</li>
          <li>Keys in gitignored <code>.env</code>; <code>verify_langfuse_traces.sh</code> smoke</li>
          <li>Complements ClickStack — does not replace API/pipeline tracing</li>
        </ul>
        <div class="pl-callout">
          HLD dotted LibreChat → Langfuse direct — actual path is <strong>via LiteLLM proxy</strong>.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Scaling to 100×</h2>
  <div class="pl-sub">Interpretation A — 100× sessions in the same 12-day window (live-event density)</div>
  <div class="pl-body">
    <div class="pl-stat-row">
      <div class="pl-stat"><div class="val">~10⁷</div><div class="lbl">delta rows</div></div>
      <div class="pl-stat"><div class="val">~420K</div><div class="lbl">segments / day</div></div>
      <div class="pl-stat"><div class="val">&lt;1 GB</div><div class="lbl">serving layer</div></div>
      <div class="pl-stat"><div class="val">50–150 ms</div><div class="lbl">filtered day query</div></div>
    </div>
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Why the model survives</h3>
        <ul>
          <li>Deltas scale with <strong>segments</strong>, not minutes — no per-minute explosion</li>
          <li>Query output = one row per clock minute in window — invariant to session count</li>
          <li>Bounded semi-join + opening balance — O(window + lookback), not O(history)</li>
          <li>Skip semi-join when no dimension filter — 3–8× on unfiltered queries</li>
          <li>Rollup <code>concurrency_minute_serving</code> when p95 &gt; 200 ms (trigger in §15.5)</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Triggers we watch</h3>
        <ul>
          <li>Rollup <code>concurrency_minute_serving</code> when p95 &gt; 200 ms</li>
          <li>Build batching when raw events &gt; 20M per cohort</li>
          <li><code>REPLACE PARTITION</code> when <code>FINAL</code> &gt; 20% of query time</li>
          <li>Numeric scaling triggers — measured, not guessed (see Assumptions slide)</li>
        </ul>
        <div class="pl-callout">
          Today every team’s queries finish in ms — judges score <strong>what we read</strong> and this 100× reasoning, not stopwatch theatre.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Assumptions &amp; limits</h2>
  <div class="pl-sub">Locked semantics · asserted preconditions · open questions (Q1–Q5)</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Lookback period (<code>MAX_SEGMENT_SPAN_HOURS = 72</code>)</h3>
        <ul>
          <li>Query template bounds <code>sel</code> + opening balance to <strong>window + 72h lookback</strong></li>
          <li><strong>Overlap bound</strong> — theorem: segments outside the window contribute a cancelling ±1 pair or nothing → answer-preserving</li>
          <li><strong>Lookback bound</strong> — asserted precondition: valid only while no segment exceeds 72h</li>
          <li><strong>Impact at scale:</strong> turns O(history) scans into O(window + 72h)</li>
          <li>Same 72h bound reused for Redis TTL / streamed lateness window</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Semantic parameters (<code>config.env</code>)</h3>
        <ul>
          <li><strong>Pause excluded</strong> — flip → ~+2% peak/avg (<code>evidence/sensitivity.md</code>)</li>
          <li><strong>Buffering included</strong> — flip → ~+34% peak, +42% avg — dominant knob</li>
          <li><strong>Grace = 90s</strong> — near-inert (0.87% of gaps &gt; 90s); not the lever that fixes wrong answers</li>
          <li><strong>Any-overlap minute attribution</strong> — highest-risk locked choice vs private ground truth</li>
          <li><strong>Average = all clock minutes</strong> in window (including zeros) — narrow filters swing more if flipped</li>
          <li><strong>UTC bucketing</strong> — day grain cuts at 00:00 UTC (05:30 IST); IST is a query-layer one-liner</li>
          <li><strong>Open segments</strong> clamped to <code>least(last_hb + grace, watermark)</code> — no phantom tail past data end</li>
        </ul>
      </div>
    </div>
    <div class="pl-cols" style="margin-top: 1rem;">
      <div class="pl-panel">
        <h3>Open questions we defaulted on</h3>
        <ul>
          <li><strong>Q1</strong> User-level concurrency? → session-level primary; §7.4 island-merge SQL ready if benchmarks ask</li>
          <li><strong>Q2</strong> Hour/day grain definition? → bucket the same minute curve (peak = max of minutes in bucket)</li>
          <li><strong>Q3</strong> Pre-aggregate rollup? → no until p95 &gt; 200 ms (measure, don’t guess)</li>
          <li><strong>Q4</strong> Product depth? → ClickStack + minimal chart; LibreChat secondary</li>
          <li><strong>Q5</strong> Split segment on dim change? → snapshot at start; 99.96% of sessions vary subtitle mid-flight</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Known limits (honest)</h3>
        <ul>
          <li>Training CSV has <strong>zero open sessions</strong> — incremental path demo via <code>replay.sh</code> watermark split</li>
          <li>LibreChat MCP writes ad-hoc SQL — not the same compiler as the chart API (answers can diverge)</li>
          <li>Kafka consumer not shipped — CSV replay + <code>streamd</code> prove the streaming design</li>
          <li>At training scale, latency cannot discriminate designs — semantics + sensitivity matrix are the score</li>
        </ul>
        <div class="pl-callout">
          Every parameter is one line in <code>config.env</code> + measured delta in <code>evidence/sensitivity.md</code> — wrong ground truth is explainable, not mysterious.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Trust &amp; evidence</h2>
  <div class="pl-sub">Why answers are defensible without a published answer key</div>
  <div class="pl-body">
    <div class="pl-cols pl-cols-3">
      <div class="pl-panel">
        <h3>Validation</h3>
        <ul>
          <li><code>cmd/validate</code> — automated invariants</li>
          <li>Delta vs minute-explosion arithmetic cross-check</li>
          <li>Hand-computed fixtures + sensitivity matrix</li>
          <li><code>cmd/reconcile</code> — incremental correction tests</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Benchmarks</h3>
        <ul>
          <li><code>cmd/bench</code> → <code>evidence/answers.json</code></li>
          <li>Query log + parts read evidence</li>
          <li><code>unseen_day.sh</code> — full pipeline on held-out day</li>
          <li>Latency recorded; semantics scored first</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Guardrails</h3>
        <ul>
          <li>SQL grants: no <code>raw_events</code>, readonly=1</li>
          <li>Locked rules R1–R10 in <code>config.env</code></li>
          <li>Same serving layer for API, bench, chat</li>
          <li>Sensitivity matrix — pause, heartbeat TTL, buffer policy</li>
          <li>Docs: presentation deck + <code>evidence/sensitivity.md</code></li>
        </ul>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Future scope</h2>
  <div class="pl-sub">Built for hackathon correctness · designed for production evolution</div>
  <div class="pl-body">
    <div class="pl-cols pl-cols-3">
      <div class="pl-panel">
        <h3>Ingestion &amp; infra</h3>
        <ul>
          <li><strong>Kafka consumer</strong> — drop-in for CSV replay; same <code>Accumulator</code> + Redis hot path</li>
          <li>Redis cluster / stream partitioning for multi-worker ingest</li>
          <li>Build batching by session-start day — restartable cohorts at 90M+ events</li>
          <li>Co-shard segments + deltas on <code>cityHash64(segment_id)</code> (§15.9)</li>
          <li><code>ALTER TABLE … REPLACE PARTITION</code> — drop <code>FINAL</code> on segments at scale</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Analytics &amp; model</h3>
        <ul>
          <li><strong>User-level concurrency</strong> — materialise §7.4 island-merge → <code>user_minute_deltas</code></li>
          <li><code>uniqCombined()</code> instead of <code>uniqExact()</code> in user rollups — HLL at billion-row scale</li>
          <li>Session curve stays exact via ±1 deltas — never heartbeat-counting</li>
          <li><code>concurrency_minute_serving</code> rollup for hot low-cardinality dims</li>
          <li><strong>Metadata registry</strong> — central catalog for dynamic <code>properties.*</code> (HLD grey box)</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Product &amp; integrations</h3>
        <ul>
          <li><strong>Custom MCP server</strong> — force API-parity query templates, not ad-hoc SQL</li>
          <li>LibreChat tool routing → same compiler as <code>/api/v1/concurrency/chart</code></li>
          <li>Richer dashboard — drill-down, alerts, live-event mode (beyond minimal UI)</li>
          <li>Langfuse eval loops — trace quality of MCP-generated analytics answers</li>
          <li>ClickStack SLO dashboards — ingest lag, p95 chart latency, parts read</li>
        </ul>
      </div>
    </div>
    <div class="pl-callout" style="margin-top: 1.25rem;">
      Nothing here requires redesign — each item extends the same four-layer HLD. Session semantics (R1–R10) and the narrow-delta primitive stay fixed.
    </div>
  </div>
</div>
