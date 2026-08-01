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

| Slide | Topic |
|-------|--------|
| Title | Pulse — foreground concurrency |
| Problem | Semantics + one primitive |
| System HLD | Full diagram (`public/hld.png`) |
| **HLD review** | Gaps vs implementation — update diagram |
| Layer 01 | Ingestion (batch vs Kafka on HLD) |
| Layer 02 | Redis — preflight + live state |
| Layer 03 | Serving — API, UI, LibreChat MCP |
| Layer 04 | ClickStack + Langfuse |
| Evidence | Validation + benchmarks |
| Close | |

## HLD image

Replace `public/hld.png` when the architecture diagram is updated.

Recommended diagram edits (also on slide “HLD review”):

- Grey out or mark **Metadata Registry** and **Custom MCP Server** as future
- Split **Cache** into Preflight vs Live state
- Dotted: LibreChat → ClickHouse MCP → DB; Backend → `properties_key_mappings` MV
- Fix Langfuse arrow: via LiteLLM proxy, not direct from LibreChat
- Kafka path: dashed “future”; solid batch pipeline for hackathon

## Build / export

```bash
npm run build    # static site in dist/
npm run export   # PDF
```
