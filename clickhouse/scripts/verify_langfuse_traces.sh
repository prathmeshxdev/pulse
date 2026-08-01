#!/usr/bin/env bash
# List recent Langfuse traces (skill audit step). Requires LANGFUSE_* in .env or shell.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
if [[ -f "${ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${ROOT}/.env" && set +a
fi

export LANGFUSE_HOST="${LANGFUSE_HOST:-${LANGFUSE_BASE_URL:-https://cloud.langfuse.com}}"

if [[ -z "${LANGFUSE_PUBLIC_KEY:-}" || -z "${LANGFUSE_SECRET_KEY:-}" ]]; then
  echo "Set LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY in .env" >&2
  exit 1
fi

echo "Recent traces (Langfuse Cloud):"
sleep 3
npx --yes langfuse-cli@latest api traces list --limit 5 2>/dev/null || \
  npx --yes langfuse-cli@latest api traces list --help
