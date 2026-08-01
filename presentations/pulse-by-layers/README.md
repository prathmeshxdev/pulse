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

| # | Slide |
|---|--------|
| 1 | Title |
| 2–3 | Problem + design position (stats, R1–R4, one primitive) |
| 4 | System HLD — full-width diagram |
| 5–7 | Layer 01 Ingestion (divider + pipeline + tables) |
| 8–10 | Layer 02 Caching (divider + preflight + live state) |
| 11–13 | Layer 03 Serving (divider + API + LibreChat) |
| 14–16 | Layer 04 Observability (divider + ClickStack + Langfuse) |
| 17–18 | Trust & evidence · close |

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
