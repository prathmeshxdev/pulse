# Pulse agent setup (2 minutes)

LibreChat does not yet support fully headless agent creation via config alone.
After `docker compose --profile chat up -d`:

1. Open http://localhost:3080 and **register** (local demo allows unverified login).

2. In the endpoint dropdown (top of chat), pick **Agents** — then open the
   **Agent Builder** side panel → **Create Agent**:
   (If Agents is missing, compose must set `ENDPOINTS=custom,agents` and
   `interface.agents.use: true` in `librechat.runtime.yaml`, then recreate
   the librechat container.)
   - Name: `Pulse Concurrency`
   - Enable MCP tool: **pulse** (required — calls the same API as the dashboard)
   - Optional: **clickhouse** for schema-only inspection
   - Instructions: paste contents of [`system_prompt.md`](system_prompt.md)
     (also mounted in the container at `/app/pulse_system_prompt.md`)

3. Ask: *"What was peak concurrency on platform ANDROID_PHONE between 2026-07-15 and 2026-07-16 UTC?"*

The agent calls **`pulse-mcp`** → `POST /api/v1/concurrency/chart` on the Go
backend — identical answers to the React dashboard.

If the API runs on the host (`go run ./cmd/server`) instead of the compose
`backend` container, set in root `.env`:

```bash
PULSE_API_URL=http://host.containers.internal:8080
```

Then recreate: `docker compose --profile chat up -d --force-recreate pulse-mcp`

## Prerequisites

```bash
# From repo root (uses CLICKHOUSE_DSN from .env):
clickhouse/scripts/setup_integrations.sh
# or manually:
clickhouse/scripts/sync_librechat_env.sh
cd backend && go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" -exec "$(cat ../clickhouse/scripts/create_readonly_user.sql)"
docker compose --profile chat up -d
```

Set at least one LLM key in root `.env`: `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`.
