# Pulse — presentation deck (Slidev)

Four-layer architecture walkthrough for Click-a-thon 2026, styled like a
Slidev “by layers” technical deck (layer intros, dark theme, click-through HLD).

## Quick start

```bash
cd presentations/pulse-by-layers
./setup.sh          # npm install
npm run dev         # http://localhost:3032
```

## Contents

16 slides total. PDF export committed at repo root:
[`pulse-by-layers.pdf`](../../pulse-by-layers.pdf).

| # | Slide |
|---|--------|
| 1 | Title |
| 2 | The problem — stats, ask, foreground-state logic |
| 3 | System HLD — full-width diagram |
| 4 | Layer 01 · Real-time ingestion |
| 5 | Layer 01 · Tables & correction states (serving tables + compacted/live) |
| 6 | Layer 02 · Caching (preflight + live state) |
| 7 | Layer 03 · Query compiler (normative query + API surface) |
| 8 | Layer 03 · Serving query & latencies (SQL + ClickHouse Query Insights) |
| 9 | Layer 03 · Dashboard & demo — incl. live dashboard screenshot |
| 10 | Layer 03 · LibreChat + MCP — incl. live chat screenshot |
| 11 | Layer 04 · Observability (ClickStack + Langfuse) |
| 12 | Scaling to 100× |
| 13 | Assumptions & limits |
| 14 | Trust & evidence |
| 15 | Future scope |

## HLD image

Replace `public/hld.png` when the architecture diagram is updated.

Recommended diagram edits (for the PNG itself):

- Grey out or mark **Metadata Registry** and **Custom MCP Server** as future
- Split **Cache** into Preflight vs Live state
- Dotted: LibreChat → ClickHouse MCP → DB; Backend → `properties_key_mappings` MV
- Langfuse: via LiteLLM proxy, not direct from LibreChat
- Kafka path: dashed “future”; solid batch pipeline for hackathon

## Build / export

```bash
npm run build    # static site in dist/
npm run export   # PDF
```
