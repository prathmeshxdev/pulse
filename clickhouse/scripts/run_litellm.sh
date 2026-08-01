#!/usr/bin/env bash
# Start Langfuse-instrumented LiteLLM on the host (passthrough to corp upstream in .env).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CFG="${ROOT}/litellm/litellm-config.yaml"
PORT="${LITELLM_PORT:-4000}"

if [[ -f "${ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${ROOT}/.env" && set +a
fi

export LANGFUSE_HOST="${LANGFUSE_HOST:-${LANGFUSE_BASE_URL:-https://cloud.langfuse.com}}"
export LITELLM_UPSTREAM_URL="${LITELLM_UPSTREAM_URL:-${LITELLM_BASE_URL:-}}"
if [[ -z "${LITELLM_UPSTREAM_URL}" ]]; then
  echo "Set LITELLM_UPSTREAM_URL or LITELLM_BASE_URL in ${ROOT}/.env" >&2
  exit 1
fi
export LANGFUSE_TRACING_ENVIRONMENT="${LANGFUSE_TRACING_ENVIRONMENT:-development}"

# macOS + corp proxy (Zscaler): Python in containers cannot verify Langfuse TLS; host works.
if [[ "$(uname -s)" == "Darwin" ]]; then
  "${ROOT}/clickhouse/scripts/export_macos_certs.sh"
  export SSL_CERT_FILE="${ROOT}/litellm/certs/ca-bundle.pem"
  export REQUESTS_CA_BUNDLE="${SSL_CERT_FILE}"
fi

if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
  echo "Set ANTHROPIC_API_KEY in shell or ${ROOT}/.env" >&2
  exit 1
fi
if [[ -z "${LANGFUSE_PUBLIC_KEY:-}" || -z "${LANGFUSE_SECRET_KEY:-}" ]]; then
  echo "Set LANGFUSE_PUBLIC_KEY + LANGFUSE_SECRET_KEY for traced proxy" >&2
  exit 1
fi

if ! command -v litellm >/dev/null 2>&1; then
  pip3 install 'litellm[proxy]' 'langfuse>=2.60.0,<3.0.0' --quiet
fi

echo "LiteLLM (Langfuse) → http://127.0.0.1:${PORT}/v1"
echo "  upstream: ${LITELLM_UPSTREAM_URL}"
echo "  langfuse: ${LANGFUSE_HOST}"
# Avoid ./langfuse/ (project docs) shadowing the langfuse Python package.
cd /tmp
exec litellm --config "$CFG" --port "$PORT" --host 0.0.0.0
