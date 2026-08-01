#!/usr/bin/env bash
# Truncate-and-replay harness (FINAL_PLAN §8.3, VALIDATION Layer 8).
#
# The training data has zero open sessions, so the incremental path cannot be
# demonstrated on it as-is. This splits the CSV at a watermark T:
#   1. load + build only events BEFORE T   → some sessions are now "open"
#   2. benchmark  (snapshot BEFORE)
#   3. append the tail (events >= T) to raw_events
#   4. reconcile the affected sessions     → curve absorbs the tail, NO rebuild
#   5. benchmark  (snapshot AFTER)
# and prints the before/after peak/avg so the incremental absorption is visible.
#
# Pure Go tools over the native protocol — works against ClickHouse Cloud.
#
# Usage: replay.sh <raw.csv> <watermark_epoch_ms> [dsn]
set -euo pipefail

RAW_CSV="${1:?usage: replay.sh <raw.csv> <watermark_epoch_ms> [dsn]}"
WM_MS="${2:?watermark epoch-ms required (e.g. midpoint of the data)}"
DSN="${3:-${CLICKHOUSE_DSN:?set CLICKHOUSE_DSN or pass dsn arg}}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BACKEND="${ROOT}/backend"
CONFIG="${ROOT}/clickhouse/scripts/config.env"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

PRE="${WORK}/pre.csv"
TAIL="${WORK}/tail.csv"
SESS="${WORK}/sessions.txt"

echo "→ splitting ${RAW_CSV} at event_timestamp = ${WM_MS}"
# Header-aware split: find the event_timestamp and video_session_id columns by name.
awk -v wm="$WM_MS" -v pre="$PRE" -v tail="$TAIL" -v sess="$SESS" '
BEGIN { FS=OFS="," }
NR==1 {
  for (i=1;i<=NF;i++){ if($i=="event_timestamp") tsc=i; if($i=="video_session_id") sic=i }
  print > pre; print > tail; next
}
{
  if ($tsc+0 < wm+0) print >> pre
  else { print >> tail; print $sic >> sess }
}' "$RAW_CSV"

echo "  pre=$(( $(wc -l < "$PRE") - 1 )) rows, tail=$(( $(wc -l < "$TAIL") - 1 )) rows"
sort -u "$SESS" -o "$SESS"
echo "  affected sessions in tail: $(wc -l < "$SESS")"

cd "$BACKEND"
echo "→ [1] load + build PRE-watermark data"
go run ./cmd/loadraw       -in "$PRE" -dsn "$DSN" -config "$CONFIG" -rebuild=true
go run ./cmd/build_segments -in "$PRE" -dsn "$DSN" -config "$CONFIG" -segments= -deltas= -watermark "$(date -u -r $((WM_MS/1000)) +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || python3 -c "import datetime;print(datetime.datetime.utcfromtimestamp($WM_MS/1000).strftime('%Y-%m-%dT%H:%M:%SZ'))")"

echo "→ [2] benchmark BEFORE"
go run ./cmd/bench -dsn "$DSN" -config "$CONFIG" -out "${WORK}/before" -sql=false

echo "→ [3] append TAIL to raw_events"
go run ./cmd/loadraw -in "$TAIL" -dsn "$DSN" -config "$CONFIG" -rebuild=false

echo "→ [4] reconcile affected sessions (incremental, no rebuild)"
go run ./cmd/reconcile -dsn "$DSN" -config "$CONFIG" -sessions - < "$SESS"

echo "→ [5] benchmark AFTER"
go run ./cmd/bench -dsn "$DSN" -config "$CONFIG" -out "${WORK}/after" -sql=false

echo
echo "=== BEFORE vs AFTER (unfiltered) ==="
for grain in minute hour day; do
  b=$(grep -A4 "\"unfiltered_${grain}\"" "${WORK}/before/answers.json" 2>/dev/null | grep -E 'peak|avg' | tr -d ' ,"' || true)
  a=$(grep -A4 "\"unfiltered_${grain}\"" "${WORK}/after/answers.json"  2>/dev/null | grep -E 'peak|avg' | tr -d ' ,"' || true)
  echo "[$grain] before: ${b//$'\n'/ }   after: ${a//$'\n'/ }"
done
echo "Full evidence: ${WORK}/before and ${WORK}/after (copy out before exit if you need it)."
