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
# Keep pipefail so failures still surface when callers wrap with `| tee`.

RAW_CSV="${1:?usage: unseen_day.sh <raw.csv> <content.csv> [dsn]}"
CONTENT_CSV="${2:?content CSV required}"
DSN="${3:-${CLICKHOUSE_DSN:?set CLICKHOUSE_DSN or pass dsn arg}}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BACKEND="${ROOT}/backend"
CONFIG="${ROOT}/clickhouse/scripts/config.env"
EVID="${ROOT}/evidence/unseen_day"
mkdir -p "$EVID"

cd "$BACKEND"
echo "→ [1/7] migrations (show_name + dict recreate; idempotent)"
go run ./cmd/pipeline       -dsn "$DSN" -migrations ../clickhouse/migrations -reload-dict

echo "→ [2/7] clear prior serving data (unseen day replaces the universe)"
go run ./cmd/pipeline -dsn "$DSN" -exec "$(cat <<'SQL'
TRUNCATE TABLE IF EXISTS sony_liv.raw_events;
TRUNCATE TABLE IF EXISTS sony_liv.session_active_segments;
TRUNCATE TABLE IF EXISTS sony_liv.minute_deltas;
TRUNCATE TABLE IF EXISTS sony_liv.user_active_segments;
TRUNCATE TABLE IF EXISTS sony_liv.user_minute_deltas;
TRUNCATE TABLE IF EXISTS sony_liv.concurrency_minute_serving;
TRUNCATE TABLE IF EXISTS sony_liv.open_session_state;
TRUNCATE TABLE IF EXISTS sony_liv.session_live_state;
TRUNCATE TABLE IF EXISTS sony_liv.properties_key_mappings;
SQL
)"

echo "→ [3/7] content_metadata + dictionary reload"
go run ./cmd/loadcontent    -in "$CONTENT_CSV" -dsn "$DSN" -config "$CONFIG"

echo "→ [4/7] raw_events (video_resolution → properties JSON)"
go run ./cmd/loadraw        -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -rebuild=true

echo "→ [5/7] segments + deltas + user grain (atomic partition swap)"
go run ./cmd/build_segments -in "$RAW_CSV" -dsn "$DSN" -config "$CONFIG" -segments= -deltas= -rebuild=true

echo "→ [6/7] refresh properties key catalog (video_resolution etc.)"
go run ./cmd/pipeline -dsn "$DSN" -exec "
INSERT INTO sony_liv.properties_key_mappings
SELECT
    source,
    distinctJSONPathsAndTypes(properties) AS paths,
    now() AS refreshed_at
FROM
(
    SELECT 'raw_events' AS source, properties
    FROM sony_liv.raw_events
    WHERE NOT empty(JSONDynamicPaths(properties))
    UNION ALL
    SELECT 'session_active_segments' AS source, properties
    FROM sony_liv.session_active_segments FINAL
    WHERE NOT empty(JSONDynamicPaths(properties))
)
GROUP BY source
"

echo "→ [7/7] validate + benchmark → ${EVID}"
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
