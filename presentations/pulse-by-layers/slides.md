---
theme: default
title: Pulse
info: |
  Pulse — Click-a-thon 2026 · Sony LIV foreground concurrency
  Four-layer architecture deck
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
  padding: 4rem 5rem !important;
}
.slidev-layout h1, .slidev-layout h2 { color: var(--rm-gold); font-weight: 700; }
.slidev-layout strong { color: var(--rm-coral); }
.bg-glow { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 0; }
.bg-glow::before {
  content: ''; position: absolute; top: -20%; right: -10%;
  width: 60vw; height: 60vw;
  background: radial-gradient(circle, rgba(244,132,95,0.15) 0%, transparent 60%);
  filter: blur(40px);
}
.title-stack {
  position: relative; z-index: 1; height: 100%;
  display: flex; flex-direction: column; justify-content: center; align-items: flex-start; text-align: left;
}
.title-eyebrow {
  font-family: 'JetBrains Mono', monospace; font-size: 0.85rem;
  letter-spacing: 0.4em; color: var(--rm-lavender); text-transform: uppercase; margin-bottom: 2rem;
}
.title-main {
  font-size: 7rem; font-weight: 800; letter-spacing: -0.04em; line-height: 0.95; margin: 0;
  background: linear-gradient(135deg, #FFD166 0%, #06D6A0 100%);
  -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent;
}
.title-sub { font-size: 1.55rem; color: var(--rm-text); margin-top: 1.2rem; max-width: 42ch; }
.title-rule { width: 4rem; height: 2px; background: var(--rm-coral); margin: 2rem 0; }
.title-byline { font-family: 'JetBrains Mono', monospace; font-size: 0.9rem; color: var(--rm-muted); }
</style>

<div class="bg-glow"></div>

<div class="title-stack">

<div class="title-eyebrow">Click-a-thon 2026 · Sony LIV</div>

<h1 class="title-main">Pulse</h1>

<div class="title-sub">
Foreground-only concurrent viewers — defensible semantics on ClickHouse Cloud.
</div>

<div class="title-rule"></div>

<div class="title-byline">
Four layers · one serving primitive · dashboard + chat + evidence
</div>

</div>

<!--
Speaker notes — Title.

- Pulse answers: **who is actively watching right now?** — not background, not paused.
- We compete on **semantics and reproducibility**, not raw latency on a 10K-session dataset.
- This deck walks the **four layers** in our HLD: ingestion → caching → serving → observability.
-->

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">The problem</h2>
  <div class="pl-sub">What “concurrent viewers” must mean</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Four conditions (all required)</h3>
        <ul>
          <li v-click>Foreground app — not background</li>
          <li v-click>Playback started — not merely opened</li>
          <li v-click>Not paused — buffering still counts</li>
          <li v-click>Within session bounds — heartbeat + end events</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>One primitive</h3>
        <p v-click>
          Minute curve from sweep-line deltas → peak / avg at minute, hour, or day.
          Same curve, same filters — grains cannot disagree.
        </p>
        <div class="pl-chip-row" v-click>
          <span class="pl-chip">~10K sessions</span>
          <span class="pl-chip">~10⁵ delta rows</span>
          <span class="pl-chip">100× scaling argument</span>
        </div>
      </div>
    </div>
  </div>
</div>

<!--
- Binding rules live in docs/FINAL_PLAN.md — this is the judge-facing summary.
- Paused vs buffering asymmetry is deliberate (viewer intent).
-->

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="li-stage" :class="`li-step-${Math.min($clicks, 1)}`">
  <span v-click></span>
  <div class="li-eyebrow">Architecture</div>
  <div class="li-title-block">
    <h1 class="li-title">System HLD</h1>
    <div class="li-subtitle">four layers · bottom to top</div>
  </div>
  <div class="li-image-wrap">
    <div class="ar-image-card obs-overview-card">
      <img class="obs-overview-image" src="/hld.png" alt="Pulse system HLD" />
    </div>
  </div>
  <div class="li-progress">
    <div class="li-dot active"></div>
    <div class="li-dot"></div>
    <div class="li-dot"></div>
    <div class="li-dot"></div>
    <span class="li-progress-label">overview</span>
  </div>
</div>

<!--
- Click to reveal full diagram.
- Read bottom-up: **Ingestion** → **Caching** → **Serving** → **Observability**.
- Solid lines = implemented today. Dotted = direct paths or future components.
-->

---
transition: fade
clicks: 4
---

<div class="bg-glow"></div>

<div class="hld-stage">
  <h2 class="hld-title">HLD review — what to change on the diagram</h2>
  <div class="hld-grid">
    <div class="hld-card warn">
      <div class="hld-badge">⚠ Not built yet — use dotted / “future”</div>
      <ul>
        <li v-click><strong>Metadata Registry</strong> — no separate service. Backend reads <code>properties_key_mappings</code> MV directly for dynamic JSON key + type catalog.</li>
        <li v-click><strong>Custom MCP Server</strong> — not implemented. LibreChat uses the <strong>official ClickHouse MCP</strong> (SSE) with read-only grants.</li>
        <li v-click><strong>Kafka + kafka workers</strong> — aspirational. Hackathon path is batch CSV (<code>loadraw</code>, <code>build_segments</code>) + optional <code>streamd</code> demo.</li>
      </ul>
    </div>
    <div class="hld-card warn">
      <div class="hld-badge">⚠ Fix arrows / labels</div>
      <ul>
        <li v-click><strong>LibreChat → Langfuse</strong> — not direct. LLM traces go <strong>LibreChat → LiteLLM proxy → Langfuse</strong> (optional).</li>
        <li v-click><strong>Backend → “Registry”</strong> — relabel as dotted <strong>Backend → properties_key_mappings MV</strong> (Refreshable MV).</li>
        <li v-click><strong>OTEL</strong> — emitted from Pulse Backend + batch CLIs; LibreChat does not send ClickStack spans.</li>
      </ul>
    </div>
    <div class="hld-card ok">
      <div class="hld-badge">✓ Correct as dotted today</div>
      <ul>
        <li v-click><strong>LibreChat → DB via CH MCP</strong> — direct read-only SQL on serving tables (<code>minute_deltas</code>, <code>session_active_segments</code>).</li>
        <li v-click><strong>Refreshable MVs</strong> — <code>mv_refresh_properties_key_mappings</code> appends key/type paths from JSON <code>properties</code> column.</li>
      </ul>
    </div>
    <div class="hld-card ok">
      <div class="hld-badge">✓ Solid paths — keep</div>
      <ul>
        <li v-click><strong>Users → Pulse UI → Backend</strong> — chart API + live replay.</li>
        <li v-click><strong>Backend → Redis → ClickHouse</strong> — two Redis roles (next slide).</li>
        <li v-click><strong>Backend → ClickStack (OTEL)</strong> — Cloud collector → <code>otel_*</code> tables.</li>
      </ul>
    </div>
  </div>
</div>

<!--
- This slide is the “honest architecture” pass for judges.
- Recommend updating the diagram: grey out Metadata Registry + Custom MCP boxes, or mark “future”.
- Split Redis into two labeled flows on the caching layer.
-->

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="li-stage" :class="`li-step-${Math.min($clicks, 1)}`">
  <span v-click></span>
  <div class="li-eyebrow">Layer 01</div>
  <div class="li-title-block">
    <h1 class="li-title">Ingestion</h1>
    <div class="li-subtitle">raw events → modeled serving tables</div>
  </div>
  <div class="li-image-wrap">
    <div class="ar-image-card obs-overview-card">
      <img class="obs-overview-image" src="/hld.png" alt="Ingestion layer" />
    </div>
  </div>
  <div class="li-progress">
    <div class="li-dot done"></div>
    <div class="li-dot active"></div>
    <div class="li-dot"></div>
    <div class="li-dot"></div>
    <span class="li-progress-label">1 / 4</span>
  </div>
</div>

---
transition: fade
clicks: 3
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 01 · Ingestion</h2>
  <div class="pl-sub">Batch pipeline (implemented) vs streaming (HLD future)</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Implemented — Go native protocol</h3>
        <ul>
          <li v-click><code>loadraw</code> → <code>raw_events</code> (MergeTree)</li>
          <li v-click><code>build_segments</code> → state machine → <code>session_active_segments</code></li>
          <li v-click>Sweep-line → <code>minute_deltas</code> (SummingMergeTree, ±1 only)</li>
          <li v-click><strong>Idempotent loads:</strong> staging + <code>REPLACE PARTITION</code> — no read gap, no double-count</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Tables in ClickHouse</h3>
        <div class="pl-chip-row">
          <span class="pl-chip">raw_events</span>
          <span class="pl-chip">session_active_segments</span>
          <span class="pl-chip">minute_deltas</span>
          <span class="pl-chip">content_dict</span>
        </div>
        <p v-click style="margin-top: 1rem;">
          <strong>Dynamic properties:</strong> JSON column on segments + refreshable MV
          <code>properties_key_mappings</code> for filter discovery.
        </p>
        <p v-click style="margin-top: 0.6rem; font-size: 0.82rem;">
          HLD shows Kafka — map to <strong>future streamd → workers</strong>; today
          <code>streamd</code> writes live state to Redis for demo replay.
        </p>
      </div>
    </div>
  </div>
</div>

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="li-stage" :class="`li-step-${Math.min($clicks, 1)}`">
  <span v-click></span>
  <div class="li-eyebrow">Layer 02</div>
  <div class="li-title-block">
    <h1 class="li-title">Caching</h1>
    <div class="li-subtitle">Redis — two distinct jobs</div>
  </div>
  <div class="li-image-wrap">
    <div class="ar-image-card obs-overview-card">
      <img class="obs-overview-image" src="/hld.png" alt="Caching layer" />
    </div>
  </div>
  <div class="li-progress">
    <div class="li-dot done"></div>
    <div class="li-dot done"></div>
    <div class="li-dot active"></div>
    <div class="li-dot"></div>
    <span class="li-progress-label">2 / 4</span>
  </div>
</div>

---
transition: fade
clicks: 4
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 02 · Caching</h2>
  <div class="pl-sub">Preflight cache + live session state — same Redis, different semantics</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>① Preflight cache (<code>internal/preflight</code>)</h3>
        <ul>
          <li v-click><strong>Singleflight</strong> — identical chart queries dedupe in-flight work</li>
          <li v-click><strong>TTL result cache</strong> — <code>PREFLIGHT_CACHE_TTL</code> (default 1m)</li>
          <li v-click>Keys: SHA256 of normative query fingerprint</li>
          <li v-click>Degrades gracefully if Redis unavailable</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>② Live state cache (<code>internal/livestate</code>)</h3>
        <ul>
          <li v-click><code>streamd</code> persists per-session Accumulator in Redis</li>
          <li v-click><strong>Fixed TTL</strong> (72h from first seen) — not sliding refresh</li>
          <li v-click><code>pulse:active</code> set → O(1) live concurrency count</li>
          <li v-click>Same state machine as batch — proven by <code>validateredis</code></li>
        </ul>
      </div>
    </div>
    <p v-click style="margin-top: 1rem; text-align: center; color: var(--rm-muted); font-size: 0.85rem;">
      <strong>HLD change:</strong> split the single “Cache” box into <em>Preflight</em> and <em>Live state</em> with two arrows from Backend.
    </p>
  </div>
</div>

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="li-stage" :class="`li-step-${Math.min($clicks, 1)}`">
  <span v-click></span>
  <div class="li-eyebrow">Layer 03</div>
  <div class="li-title-block">
    <h1 class="li-title">Serving</h1>
    <div class="li-subtitle">one curve · many consumers</div>
  </div>
  <div class="li-image-wrap">
    <div class="ar-image-card obs-overview-card">
      <img class="obs-overview-image" src="/hld.png" alt="Serving layer" />
    </div>
  </div>
  <div class="li-progress">
    <div class="li-dot done"></div>
    <div class="li-dot done"></div>
    <div class="li-dot done"></div>
    <div class="li-dot active"></div>
    <span class="li-progress-label">3 / 4</span>
  </div>
</div>

---
transition: fade
clicks: 3
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 03 · Serving</h2>
  <div class="pl-sub">Pulse Backend · UI · conversational layer</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>Pulse Backend (Go API)</h3>
        <ul>
          <li v-click><code>POST /api/v1/concurrency/chart</code> — normative query compiler</li>
          <li v-click>Semi-join on <code>segment_id</code> — dimensions never widen deltas</li>
          <li v-click><code>/schema/*</code> + CH read of <code>properties_key_mappings</code> for dynamic filters</li>
          <li v-click><code>/live</code> — Redis-backed open sessions folded into curve</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Consumers</h3>
        <ul>
          <li v-click><strong>Pulse UI</strong> — React dashboard + live replay</li>
          <li v-click><strong>LibreChat</strong> — agent + ClickHouse MCP (read-only)</li>
          <li v-click>Dotted path: NL → MCP SQL (not the same compiler as API)</li>
          <li v-click>Future: proxy agent through API for strict parity with dashboard</li>
        </ul>
        <div class="pl-chip-row" v-click>
          <span class="pl-chip">cmd/bench</span>
          <span class="pl-chip">cmd/validate</span>
          <span class="pl-chip">evidence/</span>
        </div>
      </div>
    </div>
  </div>
</div>

---
transition: fade
clicks: 1
---

<div class="bg-glow"></div>

<div class="li-stage" :class="`li-step-${Math.min($clicks, 1)}`">
  <span v-click></span>
  <div class="li-eyebrow">Layer 04</div>
  <div class="li-title-block">
    <h1 class="li-title">Observability</h1>
    <div class="li-subtitle">ClickStack + Langfuse</div>
  </div>
  <div class="li-image-wrap">
    <div class="ar-image-card obs-overview-card">
      <img class="obs-overview-image" src="/hld.png" alt="Observability layer" />
    </div>
  </div>
  <div class="li-progress">
    <div class="li-dot done"></div>
    <div class="li-dot done"></div>
    <div class="li-dot done"></div>
    <div class="li-dot active"></div>
    <span class="li-progress-label">4 / 4</span>
  </div>
</div>

---
transition: fade
clicks: 2
---

<div class="bg-glow"></div>

<div class="pl-stage">
  <h2 class="pl-title">Layer 04 · Observability</h2>
  <div class="pl-sub">Two complementary planes</div>
  <div class="pl-body">
    <div class="pl-cols">
      <div class="pl-panel">
        <h3>ClickStack (OTLP → ClickHouse Cloud)</h3>
        <ul>
          <li v-click><code>clickstack-otel-collector</code> in compose</li>
          <li v-click>Spans from API <code>concurrency.chart</code> + CLIs</li>
          <li v-click>Attributes: grain, metric, filters, row counts</li>
          <li v-click>Land in Cloud <code>otel_*</code> tables</li>
        </ul>
      </div>
      <div class="pl-panel">
        <h3>Langfuse (LLM traces)</h3>
        <ul>
          <li v-click>Optional LiteLLM proxy with Langfuse callbacks</li>
          <li v-click>LibreChat generations + MCP tool calls</li>
          <li v-click>Separate from ClickStack — conversational observability</li>
          <li v-click><code>verify_langfuse_traces.sh</code> smoke check</li>
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
  <h2 class="pl-title">Trust & evidence</h2>
  <div class="pl-sub">Why the answers are defensible</div>
  <div class="pl-body" style="display: grid; grid-template-columns: repeat(3, 1fr); gap: 1rem;">
    <div class="pl-panel">
      <h3>Validation</h3>
      <p><code>cmd/validate</code> — invariants, delta-vs-explosion cross-check, sensitivity matrix.</p>
    </div>
    <div class="pl-panel">
      <h3>Benchmarks</h3>
      <p><code>cmd/bench</code> → <code>answers.json</code> + query_log + parts evidence.</p>
    </div>
    <div class="pl-panel">
      <h3>Guardrails</h3>
      <p>Read-only MCP user — no <code>raw_events</code>, no writes. Prompt + SQL grants.</p>
    </div>
  </div>
</div>

---
transition: fade
layout: center
class: text-center
---

<div class="bg-glow"></div>

<h1 style="font-size: 4rem; background: linear-gradient(135deg, #FFD166, #06D6A0); -webkit-background-clip: text; -webkit-text-fill-color: transparent;">
  Pulse
</h1>

<p style="font-size: 1.35rem; color: #F5E9D0; max-width: 36ch; margin: 1rem auto;">
  Foreground-only concurrency · ClickHouse serving layer · four layers
</p>

<p style="font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; color: #8B95B8;">
  github.com/prathmeshxdev/pulse
</p>

<!--
- Demo: dashboard peak query, live replay, LibreChat MCP question, optional Langfuse trace.
- Questions?
-->
