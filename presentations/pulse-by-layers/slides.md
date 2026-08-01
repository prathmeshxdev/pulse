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
Orbitrons
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
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Active predicate (all four required)</h3>
        <ul>
          <li><strong>R1</strong> App in foreground — not backgrounded</li>
          <li><strong>R2</strong> Playback started — buffering counts; pause does <em>not</em></li>
          <li><strong>R3</strong> Session open — between start and end events</li>
          <li><strong>R4</strong> Heartbeat liveness — stale sessions drop off</li>
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

<div class="pl-stage">
  <h2 class="pl-title">Design position</h2>
  <div class="pl-sub">Why semantics beat stopwatch at this scale</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>At training scale (~10K sessions)</h3>
        <p>Every team's queries are fast — the dataset fits in cache. We cannot win on measured latency alone.</p>
        <ul>
          <li>Compete on <strong>defensible semantics</strong> and reproducible correctness</li>
          <li>What “actively watching” means — and why paused playback is excluded</li>
          <li>Why each benchmark answer matches what a viewer reads off the curve</li>
          <li>100× scaling argument is analytical (§15), not benchmark theatre</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>One primitive — three grains</h3>
        <p>Sweep-line deltas → cumulative minute curve → bucket for hour/day.</p>
        <ul>
          <li><code>minute_deltas</code>: narrow rows <code>(minute, segment_id, ±1)</code></li>
          <li>Dimensions on <code>session_active_segments</code> — semi-join, never widen deltas</li>
          <li>Peak = max of curve; average = mean of curve over window</li>
          <li>Minute, hour, day derived from the <strong>same</strong> curve — cannot disagree</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">SummingMergeTree</span>
          <span class="pl-chip">ReplacingMergeTree</span>
          <span class="pl-chip">content_dict</span>
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
    <div class="hld-layer-pill">01 Ingestion<span>CSV → segments + deltas</span></div>
    <div class="hld-layer-pill">02 Caching<span>preflight + live state</span></div>
    <div class="hld-layer-pill">03 Serving<span>API · UI · LibreChat</span></div>
    <div class="hld-layer-pill">04 Observability<span>ClickStack · Langfuse</span></div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="ld-stage">
  <div class="ld-content">
    <div class="ld-eyebrow">Layer 01</div>
    <h1 class="ld-title">Ingestion</h1>
    <div class="ld-subtitle">raw events → modeled serving tables on ClickHouse Cloud</div>
    <div class="ld-progress">
      <div class="ld-dot active"></div>
      <div class="ld-dot"></div>
      <div class="ld-dot"></div>
      <div class="ld-dot"></div>
      <span class="ld-progress-label">1 / 4</span>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 01 · Pipeline</h2>
  <div class="pl-sub">Pure Go · native protocol · no clickhouse-client required</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Commands</h3>
        <ul>
          <li><code>cmd/pipeline</code> — DDL migrations + dictionary reload</li>
          <li><code>cmd/loadraw</code> — hackathon CSV → <code>raw_events</code></li>
          <li><code>cmd/build_segments</code> — state machine → segments + deltas</li>
          <li><code>cmd/reconcile</code> — published-edge correction for late events</li>
          <li><code>cmd/streamd</code> — optional replay stream → Redis live state</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Segment builder</h3>
        <ul>
          <li>Shared <code>Accumulator</code> — batch and streaming use identical logic</li>
          <li>Classifies 41+ heartbeat <code>event</code> values (pause, resume, buffer, seek…)</li>
          <li>Emits <code>session_active_segments</code> with typed dims + JSON <code>properties</code></li>
          <li>Sweep-line: one <code>+1</code> / <code>−1</code> pair per active interval</li>
        </ul>
        <div class="pl-callout">
          <strong>Idempotent loads:</strong> stage table → <code>ALTER TABLE … REPLACE PARTITION</code> — readers never see an empty partition mid-rebuild; SummingMergeTree re-runs cannot double-count.
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
  <h2 class="pl-title">Layer 01 · ClickHouse tables</h2>
  <div class="pl-sub">Typed DDL · dynamic properties · refreshable MV catalog</div>
  <div class="pl-body">
    <div class="pl-cols pl-cols-3">
      <div class="pl-panel">
        <h3>Storage</h3>
        <ul>
          <li><code>raw_events</code> — MergeTree log (pipeline input)</li>
          <li><code>session_active_segments</code> — ReplacingMergeTree + version</li>
          <li><code>minute_deltas</code> — SummingMergeTree, no dimensions</li>
          <li><code>content_metadata</code> + <code>content_dict</code></li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Dynamic properties</h3>
        <ul>
          <li>JSON <code>properties</code> column on segments (CSV extras)</li>
          <li><code>properties_key_mappings</code> — daily append MV</li>
          <li>Backend reads MV for filter keys + types (no separate registry service)</li>
          <li>Filters: <code>toFloat64(properties.my_key)</code> with typed cast</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>HLD vs built</h3>
        <ul>
          <li>Kafka workers on diagram = <strong>future</strong> streaming path</li>
          <li>Today: batch CSV + optional <code>streamd</code> demo</li>
          <li>OTEL spans on <code>loadraw</code>, <code>build_segments</code>, <code>pipeline</code></li>
          <li>Cloud-native: secure port 9440, migrations in repo</li>
        </ul>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="ld-stage">
  <div class="ld-content">
    <div class="ld-eyebrow">Layer 02</div>
    <h1 class="ld-title">Caching</h1>
    <div class="ld-subtitle">Redis — preflight dedupe + live session state</div>
    <div class="ld-progress">
      <div class="ld-dot done"></div>
      <div class="ld-dot active"></div>
      <div class="ld-dot"></div>
      <div class="ld-dot"></div>
      <span class="ld-progress-label">2 / 4</span>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 02 · Preflight cache</h2>
  <div class="pl-sub">internal/preflight — singleflight + TTL result cache</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Problem it solves</h3>
        <ul>
          <li>Dashboard + bench hammer identical chart queries</li>
          <li>Without dedupe: N concurrent clients → N identical ClickHouse scans</li>
          <li>Preflight collapses in-flight work to one execution</li>
        </ul>
        <p><code>PREFLIGHT_ENABLED=true</code> · <code>PREFLIGHT_CACHE_TTL=1m</code> (configurable)</p>
      </div>
      <div class="pl-panel">
        <h3>Mechanism</h3>
        <ul>
          <li>Cache key = SHA256 of normative query fingerprint (<code>CacheKey</code>)</li>
          <li><code>SetNX</code> inflight lock → winner runs CH; losers poll or wait</li>
          <li>Result JSON stored under <code>sony:ch:result:*</code></li>
          <li>Redis down → graceful degrade to direct ClickHouse (no hard fail)</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">singleflight</span>
          <span class="pl-chip">cross-process</span>
          <span class="pl-chip">chart API</span>
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
  <h2 class="pl-title">Layer 02 · Live state</h2>
  <div class="pl-sub">internal/livestate · streamd · real-time demo path</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Second Redis role (different semantics)</h3>
        <ul>
          <li><code>streamd</code> replays CSV timeline → per-session <code>Accumulator</code> in Redis</li>
          <li>Keys: <code>pulse:session:{id}</code> — JSON snapshot of open segments</li>
          <li><strong>Fixed TTL</strong> 72h from first seen — not sliding refresh</li>
          <li><code>pulse:active</code> set — O(1) live concurrency count</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Correctness guarantees</h3>
        <ul>
          <li>Same state machine as batch — <code>TestStreamingMatchesBatch</code></li>
          <li><code>cmd/validateredis</code> vs in-memory reference on real CSV shapes</li>
          <li><code>/live</code> API folds open sessions into the minute curve</li>
          <li><code>Sweep()</code> evicts silent sessions for accurate wall-clock reads</li>
        </ul>
        <div class="pl-callout">
          Closed sessions are <strong>not</strong> deleted from Redis — real data shows events seconds after <code>VideoSessionEnd</code>; deleting would resurrect bogus segments.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="ld-stage">
  <div class="ld-content">
    <div class="ld-eyebrow">Layer 03</div>
    <h1 class="ld-title">Serving</h1>
    <div class="ld-subtitle">normative query compiler · dashboard · conversational layer</div>
    <div class="ld-progress">
      <div class="ld-dot done"></div>
      <div class="ld-dot done"></div>
      <div class="ld-dot active"></div>
      <div class="ld-dot"></div>
      <span class="ld-progress-label">3 / 4</span>
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
          <li>React dashboard (:5173) + live replay view</li>
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
  <h2 class="pl-title">Layer 03 · LibreChat + MCP</h2>
  <div class="pl-sub">Second consumer of the serving layer (dotted path on HLD)</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>What we built</h3>
        <ul>
          <li>LibreChat + official <strong>ClickHouse MCP</strong> (SSE, :8001)</li>
          <li>Read-only user <code>pulse_readonly</code> — no <code>raw_events</code>, no writes</li>
          <li><code>system_prompt.md</code> — serving tables, semantics, example SQL</li>
          <li>Compose profile <code>chat</code> — MongoDB + MCP + LiteLLM endpoint</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Honest limits</h3>
        <ul>
          <li>Agent writes raw SQL — <strong>not</strong> the same compiler as the API</li>
          <li>Same tables &amp; semantics; different query logic → answers can diverge</li>
          <li>No custom MCP proxy yet (future: force API-parity templates)</li>
          <li>Metadata for dynamic keys: agent queries <code>properties_key_mappings</code> or uses prompt catalog</li>
        </ul>
        <div class="pl-callout">
          Example: <em>“Peak concurrency on ANDROID_PHONE between 13:00–14:00 UTC?”</em> → MCP tool → <code>minute_deltas</code> + semi-join.
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="ld-stage">
  <div class="ld-content">
    <div class="ld-eyebrow">Layer 04</div>
    <h1 class="ld-title">Observability</h1>
    <div class="ld-subtitle">ClickStack traces · Langfuse LLM observability</div>
    <div class="ld-progress">
      <div class="ld-dot done"></div>
      <div class="ld-dot done"></div>
      <div class="ld-dot done"></div>
      <div class="ld-dot active"></div>
      <span class="ld-progress-label">4 / 4</span>
    </div>
  </div>
</div>

---
transition: fade
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 04 · ClickStack</h2>
  <div class="pl-sub">OTLP → ClickHouse Cloud · pipeline &amp; API spans</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Setup</h3>
        <ul>
          <li><code>clickstack-otel-collector</code> in root docker-compose</li>
          <li><code>observability.env</code> from <code>sync_librechat_env.sh</code></li>
          <li>Backend: <code>OTEL_EXPORTER_OTLP_ENDPOINT=http://clickstack:4318</code></li>
          <li>Spans land in Cloud <code>otel_*</code> tables</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>What we trace</h3>
        <ul>
          <li><code>concurrency.chart</code> — grain, metric, filters, result rows, errors</li>
          <li><code>loadraw</code>, <code>build_segments</code>, <code>pipeline</code> CLIs</li>
          <li>Service name: <code>pulse-concurrency-api</code></li>
          <li><code>smoke_integrations.sh</code> — API, CH, OTLP, MCP health</li>
        </ul>
        <div class="pl-chip-row">
          <span class="pl-chip">OpenTelemetry</span>
          <span class="pl-chip">ClickHouse Cloud</span>
          <span class="pl-chip">internal/otelx</span>
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
  <h2 class="pl-title">Layer 04 · Langfuse</h2>
  <div class="pl-sub">LLM traces — separate plane from ClickStack</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Architecture</h3>
        <ul>
          <li>LibreChat → optional local LiteLLM proxy (:4000)</li>
          <li>LiteLLM <code>success_callback: ["langfuse"]</code></li>
          <li>Traces: prompts, tokens, MCP tool calls</li>
          <li>Langfuse Cloud (or self-host script in repo)</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Ops notes</h3>
        <ul>
          <li>Org upstream URL + keys in gitignored <code>.env</code> only</li>
          <li>macOS + Podman: host-run LiteLLM if container TLS fails</li>
          <li><code>verify_langfuse_traces.sh</code> — post-chat smoke</li>
          <li>Complements ClickStack — does not replace API/pipeline tracing</li>
        </ul>
        <div class="pl-callout">
          HLD shows LibreChat → Langfuse direct — actual path is <strong>via LiteLLM proxy</strong> when Langfuse keys are configured.
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
          <li>Locked rules R1–R10 in <code>FINAL_PLAN.md</code></li>
          <li>Same serving layer for API, bench, chat</li>
          <li>Docs: semantics spec + architecture HLD</li>
        </ul>
      </div>
    </div>
  </div>
</div>

---
transition: fade
layout: center
class: text-center
---

<div class="bg-glow"></div>

<h1 style="font-size: 3.8rem; background: linear-gradient(135deg, #FFD166, #06D6A0); -webkit-background-clip: text; -webkit-text-fill-color: transparent; font-weight: 800;">
  Pulse
</h1>

<p style="font-size: 1.2rem; color: #F5E9D0; max-width: 40ch; margin: 1rem auto; line-height: 1.45;">
  Foreground-only concurrency · four layers · ClickHouse Cloud · defensible semantics
</p>

<p style="font-family: 'JetBrains Mono', monospace; font-size: 0.82rem; color: #8B95B8; margin-top: 1.5rem;">
  Orbitrons · github.com/prathmeshxdev/pulse
</p>
