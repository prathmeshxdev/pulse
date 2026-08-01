-- Incremental correction for one session's segments, WITHOUT a full rebuild.
-- FINAL_PLAN §8.2 / SCHEMA_AND_DDL "Delta corrections without waiting for merge".
--
-- The runnable executor is `cmd/reconcile` (Go): it recomputes the session's
-- segments and applies the correction below. This file documents the two SQL
-- steps that make the correction idempotent and merge-independent.
--
-- Parameters: {segment_ids} = the affected segment_id set (old ∪ new).

-- Step 1 — read the currently PUBLISHED edge back out of minute_deltas.
-- This is the source of truth for what to cancel. It must NOT be recomputed
-- from open_session_state (already overwritten) — reading it back is what makes
-- re-running reconcile a no-op. Merge-independent: sum(delta) collapses parts.
SELECT minute, segment_id, sum(delta) AS d
FROM sony_liv.minute_deltas
WHERE segment_id IN ({segment_ids:Array(UInt64)})
GROUP BY minute, segment_id
HAVING d <> 0;

-- Step 2 — cancel each published edge (insert its negation) and insert the
-- newly-computed edges. SummingMergeTree collapses (published) + (−published)
-- to zero and leaves the new edge. Running twice reads the new edge as the
-- published one, cancels it, and rewrites it → still correct.
--
--   INSERT INTO sony_liv.minute_deltas (minute, segment_id, delta)
--   -- cancellations: one row per published edge with negated delta
--   VALUES (...), ...;
--
--   INSERT INTO sony_liv.minute_deltas (minute, segment_id, delta)
--   -- new any-overlap edges for the recomputed segments
--   VALUES (...), ...;
--
-- And replace the segment rows (ReplacingMergeTree(version), higher version):
--   INSERT INTO sony_liv.session_active_segments (...) VALUES (...);
