#!/usr/bin/env bash
# Smoke-test ClickStack OTLP + LibreChat MCP + Pulse API against Cloud.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
if [[ -f "${ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${ROOT}/.env" && set +a
fi

API="${PULSE_API:-http://localhost:8080}"
OTEL="${OTEL_EXPORTER_OTLP_ENDPOINT:-}"
MCP="${LIBRECHAT_MCP_URL:-http://localhost:8001/sse}"
DSN="${CLICKHOUSE_DSN:-}"

pass=0
fail=0
check() {
  local name="$1" rc="$2"
  if [[ "$rc" -eq 0 ]]; then
    echo "  [PASS] $name"
    pass=$((pass + 1))
  else
    echo "  [FAIL] $name"
    fail=$((fail + 1))
  fi
}

echo "=== Pulse integration smoke test ==="

if [[ -n "$DSN" ]]; then
  cd "${ROOT}/backend"
  go run ./cmd/pipeline -dsn "$DSN" -exec "SELECT count() FROM sony_liv.minute_deltas" >/dev/null 2>&1
  check "ClickHouse Cloud (minute_deltas reachable)" $?
  go run ./cmd/pipeline -dsn "$DSN" -exec "SELECT count() FROM sony_liv.properties_key_mappings" >/dev/null 2>&1
  check "properties_key_mappings table exists" $?
else
  echo "  [SKIP] ClickHouse (CLICKHOUSE_DSN unset)"
fi

curl -sf "${API}/health" >/dev/null 2>&1
check "Pulse API /health" $?

curl -sf "${API}/api/v1/schema/dimensions" >/dev/null 2>&1
check "Pulse API /schema/dimensions" $?

if [[ -n "$OTEL" ]]; then
  curl -sf -o /dev/null -X POST "${OTEL}/v1/traces" -H 'Content-Type: application/json' -d '{}' 2>/dev/null
  # HyperDX may return 4xx on empty body; reachable if not connection refused.
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${OTEL}/v1/traces" -H 'Content-Type: application/json' -d '{}' 2>/dev/null || echo "000")
  if [[ "$code" != "000" ]]; then
    check "ClickStack OTLP endpoint reachable ($OTEL, HTTP $code)" 0
  else
    check "ClickStack OTLP endpoint reachable ($OTEL)" 1
  fi
else
  echo "  [SKIP] ClickStack (OTEL_EXPORTER_OTLP_ENDPOINT unset — start: docker compose --profile observability up -d)"
fi

mcp_code=$(curl -s -o /dev/null -w '%{http_code}' "$MCP" 2>/dev/null || echo "000")
if [[ "$mcp_code" != "000" ]]; then
  check "LibreChat ClickHouse MCP SSE ($MCP, HTTP $mcp_code)" 0
else
  echo "  [SKIP] LibreChat MCP (not running — start: docker compose --profile chat up -d)"
fi

echo
echo "Result: ${pass} passed, ${fail} failed"
[[ "$fail" -eq 0 ]]
