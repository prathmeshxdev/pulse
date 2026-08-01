#!/usr/bin/env bash
# Drop sony_liv, recreate all tables, run the full pipeline + validation.
#
# Usage: reset_and_simulate.sh [raw.csv] [content.csv] [dsn]
# Defaults: SonyLiv hackathon CSVs; DSN from CLICKHOUSE_DSN / .env
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BACKEND="${ROOT}/backend"
CONFIG="${ROOT}/clickhouse/scripts/config.env"
EVID="${ROOT}/evidence/simulation"
RAW_CSV="${1:-/Users/prathmesh/Projects/click-a-thon-2026/SonyLiv/data/ch-hackathon-raw-data.csv}"
CONTENT_CSV="${2:-/Users/prathmesh/Projects/click-a-thon-2026/SonyLiv/data/ch-hackathon-content-data.csv}"

if [[ -f "${ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${ROOT}/.env" && set +a
fi
DSN="${3:-${CLICKHOUSE_DSN:?set CLICKHOUSE_DSN or pass dsn arg}}"

for f in "$RAW_CSV" "$CONTENT_CSV"; do
  [[ -f "$f" ]] || { echo "missing file: $f" >&2; exit 1; }
done

mkdir -p "$EVID"
cd "$BACKEND"

echo "=== [1/7] drop + migrate ==="
go run ./cmd/pipeline -dsn "$DSN" -migrations ../clickhouse/migrations -drop -reload-dict

echo "=== [2/7] content_metadata + dictionary ==="
go run ./cmd/loadcontent -in "$CONTENT_CSV" -dsn "$DSN" -config "$CONFIG"

echo "=== [3/7] raw_events ==="
go run ./cmd/loadraw -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -rebuild=true

echo "=== [4/7] segments + deltas ==="
go run ./cmd/build_segments -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -segments= -deltas= -rebuild=true

echo "=== [5/7] refresh properties key mappings (post bulk load) ==="
go run ./cmd/pipeline -dsn "$DSN" -exec "SYSTEM REFRESH VIEW sony_liv.mv_refresh_properties_key_mappings"

echo "=== [6/7] validate ==="
go run ./cmd/validate -dsn "$DSN" -in "$RAW_CSV" -config "$CONFIG" -out "$EVID"

echo "=== [7/7] benchmark ==="
go run ./cmd/bench -dsn "$DSN" -config "$CONFIG" -out "$EVID" -sql=false

echo
echo "=== simulation complete → ${EVID} ==="
ls -la "$EVID"
