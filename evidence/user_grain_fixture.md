# User-grain concurrency evidence

Session-independent peak concurrency merges overlapping `session_active_segments` per `user_id` into islands, then applies the same sweep-line ±1 delta model.

## Fixture: two sessions, one user

Automated in `backend/internal/users/merge_test.go` — `TestFixture_TwoSessionsOneUser`.

| Window | Session-aware peak | User-level peak |
|--------|-------------------:|----------------:|
| 2026-01-15 10:00–11:00 UTC | **2** (overlap 10:10–10:20) | **1** |

User `u-overlap` watches on sessions `s1` (10:00–10:30) and `s2` (10:10–10:20) concurrently.

## Pipeline

```bash
# After build_segments (also runs automatically at end of build_segments -dsn ...)
go run ./cmd/build_user_segments -dsn "$CLICKHOUSE_DSN" -config ../clickhouse/scripts/config.env
```

## Query API / bench

```json
{ "unit": "user", "grain": "minute", "metric": "summary", "start": "...", "end": "..." }
```

Default bench case: `unfiltered_minute_user`.

Tables: `user_active_segments`, `user_minute_deltas`.
