#!/usr/bin/env bash
# Bulk-load Sony LIV CSVs into raw_events + content_metadata.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck disable=SC1091
source "${ROOT}/clickhouse/scripts/config.env"

CH_HOST="${CH_HOST:-localhost}"
CH_PORT="${CH_PORT:-9000}"
CH_USER="${CH_USER:-default}"
CH_PASSWORD="${CH_PASSWORD:-}"
RAW_CSV="${1:?usage: load_data.sh <raw.csv> [content.csv]}"
CONTENT_CSV="${2:-}"

client() {
  if [[ -n "${CH_PASSWORD}" ]]; then
    clickhouse-client --host "$CH_HOST" --port "$CH_PORT" --user "$CH_USER" --password "$CH_PASSWORD" "$@"
  else
    clickhouse-client --host "$CH_HOST" --port "$CH_PORT" --user "$CH_USER" "$@"
  fi
}

echo "Loading raw events from ${RAW_CSV}"
client --query "
INSERT INTO ${DATABASE}.raw_events (
  video_session_id, user_id, content_id, event_type, event, event_timestamp,
  platform, app_version, country, audio_language, subtitle_language, player_version,
  session_start_epoch, properties
)
SELECT
  video_session_id,
  user_id,
  toUInt64OrZero(content_id),
  event_type,
  event,
  fromUnixTimestamp64Milli(toInt64(event_timestamp)),
  platform,
  app_version,
  country,
  audio_language,
  subtitle_language,
  player_version,
  if(session_start_epoch = '', fromUnixTimestamp64Milli(toInt64(event_timestamp)),
     fromUnixTimestamp64Milli(toInt64(session_start_epoch))),
  CAST('{}' AS JSON)
FROM input('
  video_session_id String,
  user_id String,
  content_id String,
  event_type String,
  event String,
  event_timestamp String,
  platform String,
  app_version String,
  country String,
  audio_language String,
  subtitle_language String,
  player_version String,
  session_start_epoch String
')
FORMAT CSVWithNames
" < "${RAW_CSV}"

if [[ -n "${CONTENT_CSV}" ]]; then
  echo "Loading content metadata from ${CONTENT_CSV}"
  client --query "
  INSERT INTO ${DATABASE}.content_metadata (content_id, title, video_type, category, show_name)
  SELECT toUInt64(content_id), title, video_type, category, show_name
  FROM input('content_id String, title String, video_type String, category String, show_name String')
  FORMAT CSVWithNames
  " < "${CONTENT_CSV}"
  client --query "SYSTEM RELOAD DICTIONARY ${DATABASE}.content_dict"
fi

client --query "SELECT count() AS raw_events FROM ${DATABASE}.raw_events"
echo "load complete"
