# Pulse agent setup (2 minutes)

LibreChat does not yet support fully headless agent creation via config alone.
After `docker compose --profile chat up -d`:

1. Open http://localhost:3080 and **register** (local demo allows unverified login).

2. **Agents** → **Create Agent**:
   - Name: `Pulse Concurrency`
   - Enable MCP tool: **clickhouse**
   - Instructions: paste contents of [`system_prompt.md`](system_prompt.md)
     (also mounted in the container at `/app/pulse_system_prompt.md`)

3. Ask: *"What was peak concurrency on platform ANDROID_PHONE between 2026-07-15 and 2026-07-16 UTC?"*

The agent queries **ClickHouse Cloud** via the readonly `pulse_readonly` user —
same serving layer as the dashboard (`minute_deltas` + `session_active_segments`).

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
