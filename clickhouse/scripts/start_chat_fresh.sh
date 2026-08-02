#!/usr/bin/env bash
# Fresh LibreChat stack — host API/LiteLLM/pulse-mcp + compose Mongo/MCP/LibreChat UI.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if command -v podman-compose >/dev/null 2>&1; then
  COMPOSE=(podman-compose)
elif docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
else
  COMPOSE=(docker-compose)
fi

[[ -f .env ]] && set -a && source .env && set +a

echo "→ podman machine"
podman machine start 2>/dev/null || true

echo "→ sync librechat env"
"${ROOT}/clickhouse/scripts/sync_librechat_env.sh" >/dev/null

# Hybrid macOS: LiteLLM + pulse-mcp on host; sync would point at in-compose litellm.
cat > "${ROOT}/librechat/librechat.runtime.yaml" <<'YAML'
# LibreChat runtime — host LiteLLM + pulse-mcp; ClickHouse MCP in compose.
version: 1.2.1

mcpSettings:
  allowedAddresses:
    - "clickhouse-mcp:8001"
    - "pulse-mcp:8002"

interface:
  modelSelect: true
  parameters: true
  presets: true
  agents:
    use: true
    create: true
    share: false
    public: false
  mcpServers:
    use: true
    share: false
    create: false
    public: false

endpoints:
  agents:
    disableBuilder: false
    capabilities:
      - tools
      - actions
      - context
  custom:
    - name: "LiteLLM"
      apiKey: "sk-pulse-litellm-local"
      baseURL: "http://host.docker.internal:4000/v1"
      models:
        default: ["claude-sonnet-5"]
        fetch: true
      titleConvo: true
      titleModel: "claude-sonnet-5"
      summarize: false
      modelDisplayLabel: "LiteLLM"

mcpServers:
  pulse:
    type: sse
    url: http://pulse-mcp:8002/sse
    description: Pulse chart/breakdown API (same compiler as dashboard).
  clickhouse:
    type: sse
    url: http://clickhouse-mcp:8001/sse
    description: Optional ClickHouse schema inspection.
YAML

echo "→ ensure host services (API, LiteLLM, pulse-mcp)"
redis-cli ping >/dev/null 2>&1 || redis-server --daemonize yes --port 6379

if ! curl -sf http://localhost:8080/health >/dev/null 2>&1; then
  echo "  starting API on :8080 (background) — run manually if this fails"
  (cd backend && REDIS_ADDR=localhost:6379 go run ./cmd/server) &
fi
if ! curl -sf http://localhost:4000/health/liveliness >/dev/null 2>&1; then
  echo "  starting LiteLLM on :4000 (background)"
  "${ROOT}/clickhouse/scripts/run_litellm.sh" &
  sleep 3
fi
if ! curl -sf http://localhost:8002/health >/dev/null 2>&1; then
  echo "  pulse-mcp will run in compose (not on host)"
fi

echo "→ tear down old chat containers"
"${COMPOSE[@]}" --profile chat down --remove-orphans 2>/dev/null || true
podman rm -f pulse_librechat_1 pulse_librechat-mongodb_1 pulse_clickhouse-mcp_1 \
  pulse_pulse-mcp_1 pulse_litellm_1 2>/dev/null || true

echo "→ readonly ClickHouse user"
(cd backend && go run ./cmd/pipeline -dsn "$CLICKHOUSE_DSN" \
  -exec "$(cat ../clickhouse/scripts/create_readonly_user.sql)") >/dev/null

echo "→ start chat stack (mongo + clickhouse-mcp + pulse-mcp + librechat)"
PULSE_API_URL=http://host.containers.internal:8080 \
  "${COMPOSE[@]}" --profile chat up -d librechat-mongodb clickhouse-mcp pulse-mcp librechat

echo "→ waiting for UI"
for i in $(seq 1 30); do
  if curl -sf -o /dev/null http://localhost:3080/; then
    echo ""
    echo "LibreChat → http://localhost:3080"
    echo "Agent prompt → librechat/system_prompt.md"
    echo "Enable MCP: pulse (+ optional clickhouse)"
    exit 0
  fi
  sleep 2
done
echo "LibreChat not up yet — check: podman logs pulse_librechat_1" >&2
exit 1
