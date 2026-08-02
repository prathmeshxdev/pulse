#!/usr/bin/env bash
# One-shot: sync LibreChat env, create readonly user, start ClickStack + LibreChat.
# Uses podman-compose when available, else docker-compose.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if command -v podman-compose >/dev/null 2>&1; then
  COMPOSE=(podman-compose)
elif docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  echo "Install podman-compose or docker compose" >&2
  exit 1
fi

if [[ ! -f .env ]]; then
  echo "Copy .env.example → .env and set CLICKHOUSE_DSN (Cloud) + an LLM API key." >&2
  exit 1
fi

# shellcheck disable=SC1091
set -a && source .env && set +a

echo "→ sync librechat env from CLICKHOUSE_DSN"
"${ROOT}/clickhouse/scripts/sync_librechat_env.sh"

echo "→ create pulse_readonly user on ClickHouse Cloud"
cd backend
go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" -exec "$(cat ../clickhouse/scripts/create_readonly_user.sql)"

echo "→ start ClickStack + LibreChat (profile: full)"
cd "$ROOT"
"${COMPOSE[@]}" --profile full up -d clickstack librechat-mongodb clickhouse-mcp pulse-mcp librechat

echo
echo "=== Integration stack ==="
echo "  HyperDX UI:     http://localhost:8081"
echo "  OTLP endpoint:  http://localhost:4318  (set OTEL_EXPORTER_OTLP_ENDPOINT in .env)"
echo "  LibreChat UI:   http://localhost:3080  (register, then create Agent — see librechat/AGENT_SETUP.md)"
echo "  ClickHouse MCP: http://localhost:8001/sse"
echo "  Pulse API MCP:  http://localhost:8002/sse  (chart/breakdown — same as dashboard)"
echo
echo "Start Pulse API with tracing:"
echo "  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 docker compose up backend frontend redis"
echo
echo "Smoke test:"
echo "  clickhouse/scripts/smoke_integrations.sh"
