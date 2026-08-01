#!/usr/bin/env bash
# Self-host Langfuse locally (optional). UI: http://localhost:3000
# Prefer Langfuse Cloud for hackathon — see langfuse/README.md
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DIR="${LANGFUSE_DIR:-${ROOT}/.langfuse-upstream}"

if [[ ! -d "${DIR}/docker-compose.yml" && ! -f "${DIR}/docker-compose.yml" ]]; then
  echo "→ cloning langfuse into ${DIR}"
  git clone --depth 1 https://github.com/langfuse/langfuse.git "$DIR"
fi

echo "→ starting Langfuse (Postgres + ClickHouse + Redis + MinIO) — first boot ~2–3 min"
echo "  UI: http://localhost:3000"
echo "  Update secrets marked CHANGEME in ${DIR}/docker-compose.yml before prod use."
cd "$DIR"
exec podman-compose up -d 2>/dev/null || docker compose up -d
