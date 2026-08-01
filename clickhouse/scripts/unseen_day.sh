#!/usr/bin/env bash
# One-command unseen-day runner: raw CSV + content CSV → full pipeline → evidence.
# Satisfies "your submission must include your system's answers on the unseen day,
# the query latencies, and evidence that they ran through your pipeline."
#
# Pure Go over the native protocol — works against ClickHouse Cloud, no
# clickhouse-client needed. Idempotent (migrations use IF NOT EXISTS; loads use
# atomic REPLACE PARTITION).
#
# Usage: unseen_day.sh <raw.csv> <content.csv> [dsn]
set -euo pipefail

RAW_CSV="${1:?usage: unseen_day.sh <raw.csv> <content.csv> [dsn]}"
CONTENT_CSV="${2:?content CSV required}"
DSN="${3:-${CLICKHOUSE_DSN:?set CLICKHOUSE_DSN or pass dsn arg}}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BACKEND="${ROOT}/backend"
CONFIG="${ROOT}/clickhouse/scripts/config.env"
EVID="${ROOT}/evidence/unseen_day"
mkdir -p "$EVID"

cd "$BACKEND"
echo "→ [1/5] migrations (idempotent)"
go run ./cmd/pipeline       -dsn "$DSN" -migrations ../clickhouse/migrations -reload-dict

echo "→ [2/5] content_metadata + dictionary"
go run ./cmd/loadcontent    -in "$CONTENT_CSV" -dsn "$DSN" -config "$CONFIG"

echo "→ [3/5] raw_events"
go run ./cmd/loadraw        -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -rebuild=true

echo "→ [4/5] segments + deltas (atomic partition swap)"
go run ./cmd/build_segments -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -segments= -deltas= -rebuild=true

echo "→ [5/5] validate + benchmark → ${EVID}"
go run ./cmd/validate       -dsn "$DSN" -in "$RAW_CSV" -config "$CONFIG" -out "$EVID"
go run ./cmd/bench          -dsn "$DSN" -config "$CONFIG" -out "$EVID" -sql=false

echo
echo "=== unseen-day answers ==="
if command -v python3 >/dev/null; then
  python3 - "$EVID/answers.json" <<'PY'
import json,sys
for a in json.load(open(sys.argv[1])):
    c=a["case"]; p=a.get("peak"); v=a.get("avg")
    print(f"  {c['name']:<26} grain={c.get('grain','-'):<6} peak={p} avg={round(v,3) if isinstance(v,(int,float)) else v} {a['latency_ms']:.0f}ms")
PY
fi
echo
echo "Evidence bundle: ${EVID}/{answers,invariants,sensitivity,parts,query_log}"
echo "→ answers.json (results), invariants.json (correctness), sensitivity.md,"
echo "  query_log.json (rows read + server-side latency), parts.json (part counts)."
