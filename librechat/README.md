# LibreChat + ClickHouse MCP (conversational layer)

A natural-language interface over the concurrency serving layer, satisfying the
problem's "meaningfully integrate LibreChat + the ClickHouse MCP server"
requirement. Ask *"peak concurrency on Android in the last hour?"* and the agent
queries ClickHouse through the official [ClickHouse MCP server](https://github.com/ClickHouse/mcp-clickhouse).

## Setup

1. **Create the read-only ClickHouse user** — run
   [`../clickhouse/scripts/create_readonly_user.sql`](../clickhouse/scripts/create_readonly_user.sql).
   This is an **enforced** guardrail, verified on Cloud:

   | Query | Result |
   |---|---|
   | `SELECT … FROM minute_deltas` | ✅ allowed |
   | `SELECT … FROM raw_events` | ❌ `ACCESS_DENIED` (no grant) |
   | any write / `TRUNCATE` | ❌ `ACCESS_DENIED` (`readonly = 1`) |

   So even if the model is prompted adversarially, it physically cannot read the
   raw log or mutate the serving layer.

2. `cp .env.example .env` and fill CH creds + one LLM API key.

3. `docker compose up -d`

4. Open http://localhost:3080, create an account, and make an **Agent** with the
   `clickhouse` MCP tool enabled. Paste [`system_prompt.md`](system_prompt.md)
   as its instructions.

5. Ask: *"What was peak concurrency on platform ANDROID between 13:00 and 14:00
   UTC?"* — the agent calls the MCP tool and answers from the serving layer.

## Why this is meaningful, not superficial

- The agent reads the **modeled serving layer** (segments + deltas), not raw
  events, so its answers use the same foreground-only semantics as the API.
- The read-only CH user + the "never query raw_events" instruction are enforced
  guardrails, not just prompt text.
- It is a genuine second consumer of the same serving layer the dashboard uses.

## Alternative: backend proxy mode

Instead of direct MCP-to-ClickHouse, you can point an agent at the Go API
(`POST /api/v1/concurrency/chart`) so every NL query compiles to the **exact
normative template** the dashboard uses. That guarantees the chat and the
dashboard can never disagree. The MCP mode above is simpler to stand up; the
proxy mode is stricter. Both are documented in `docs/IMPLEMENTATION_PLAN.md`.
