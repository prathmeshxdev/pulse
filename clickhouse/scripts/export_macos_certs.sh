#!/usr/bin/env bash
# Build a CA bundle for LiteLLM → Langfuse Cloud TLS inside Podman on macOS (Zscaler corp proxy).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${ROOT}/litellm/certs/ca-bundle.pem"
mkdir -p "$(dirname "$OUT")"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS only — on Linux the container image CA store is usually sufficient" >&2
  exit 0
fi

# Start from Mozilla certifi (Python 3.13 rejects some full keychain exports).
CERTIFI=""
for py in python3 python; do
  CERTIFI="$("$py" -c "import certifi; print(certifi.where())" 2>/dev/null)" && break || true
done
if [[ -z "$CERTIFI" || ! -f "$CERTIFI" ]]; then
  echo "certifi not found — pip install certifi" >&2
  exit 1
fi

cp "$CERTIFI" "$OUT"
# Append Zscaler MITM roots (required behind corp proxy; avoids dumping entire keychain).
security find-certificate -a -c "Zscaler" -p >> "$OUT" 2>/dev/null || true

echo "wrote ${OUT} ($(wc -c < "$OUT" | tr -d ' ') bytes)"
