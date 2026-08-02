# LibreChat + ClickHouse MCP (conversational layer)

A natural-language interface over the concurrency serving layer. Ask *"peak
concurrency on Android in the last hour?"* and the agent calls the **Pulse chart
API** via [`pulse-mcp`](pulse-mcp/) — same Go compiler as the dashboard.

## Setup (root compose — preferred)

```bash
# From repo root — add to .env (gitignored; do not commit corp LiteLLM URLs):
#   LITELLM_BASE_URL=https://your-litellm-host/v1
#   LITELLM_API_KEY=sk-...
clickhouse/scripts/sync_librechat_env.sh
clickhouse/scripts/setup_integrations.sh

# Or step by step:
clickhouse/scripts/sync_librechat_env.sh
cd backend && go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" \
  -exec "$(cat ../clickhouse/scripts/create_readonly_user.sql)"
docker compose --profile chat up -d
```

Then follow [`AGENT_SETUP.md`](AGENT_SETUP.md): open http://localhost:3080, create
an Agent with the **`pulse`** MCP tool, paste [`system_prompt.md`](system_prompt.md).

## Legacy sidecar

`librechat/docker-compose.yml` + `librechat/.env` still works if you prefer an
isolated stack. Root `docker compose --profile chat` is the integrated path.

## Guardrails (enforced in SQL)

| Query | Result |
|---|---|
| `SELECT … FROM minute_deltas` | allowed |
| `SELECT … FROM properties_key_mappings` | allowed (dynamic dim catalog) |
| `SELECT … FROM raw_events` | `ACCESS_DENIED` |
| any write / `TRUNCATE` | `ACCESS_DENIED` (`readonly = 1`) |

See [`../clickhouse/scripts/create_readonly_user.sql`](../clickhouse/scripts/create_readonly_user.sql).

