// Package livestate implements the real-time Redis-backed streaming path:
// per-session Accumulator state is persisted in Redis with a FIXED
// (non-refreshing) TTL equal to MAX_SEGMENT_SPAN_HOURS — the same 72h bound
// already used elsewhere as the R9 query lookback (config.Constants.
// MaxSegmentSpanHours), comfortably above the measured 43.64h max session
// span in the training data (~28h of margin). The deadline is set ONCE, when
// a session's key is first created, and never extended on later writes — a
// session's Redis footprint always dies at (first-seen + 72h) regardless of
// how much it chatters, which is a simpler and stricter guarantee than a
// sliding TTL (no possibility of a pathological session staying resident
// forever via constant refresh) while still safely covering every session
// this dataset has ever produced. Events arriving after a session's key has
// expired are treated as reconcile-or-drop (see presentation deck assumptions slide).
//
// Design (validated in internal/segments and via cmd/validateredis against
// both synthetic and real production-shaped raw_events):
//   - Accumulator.Apply/Finalize is the SAME state machine as the batch
//     builder (TestStreamingMatchesBatch proves byte-identical output).
//   - Snapshot/Restore round-trips through JSON with zero behavior drift
//     (TestSnapshotRoundTrip).
//   - Closed sessions are NOT deleted (see Save doc) — real data has cases of
//     a session_id seeing further events seconds after its own
//     VideoSessionEnd; deleting the key would resurrect it as a fresh,
//     non-closed session and fabricate segments that don't exist in batch.
//   - "Active now" is a query over Accumulator.Active(now), maintained
//     incrementally via a Redis set so counting is O(1), not O(sessions) —
//     but the set is only touched on WRITES. A session that goes silent past
//     the heartbeat grace with no closing event won't drop out until its own
//     next event arrives, so an accurate wall-clock read requires Sweep()
//     first (see Sweep doc) — this was caught by cmd/validateredis comparing
//     against an in-memory reference on real data.
package livestate

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

const (
	stateKeyPrefix = "pulse:session:"
	activeSetKey   = "pulse:active"
)

// Store persists per-session Accumulator state in Redis and maintains an
// active-session set for O(1) live counting.
type Store struct {
	rdb *redis.Client
	cfg config.Constants
	ttl time.Duration

	sweepMu   sync.Mutex
	lastSweep time.Time
}

// New creates a Store. ttl is the FIXED (non-refreshing) key lifetime — the
// maximum lateness window a session's state can be corrected within. When
// zero, it defaults to cfg.MaxSegmentSpanHours (72h in the locked config),
// deliberately reusing the same bound the R9 query lookback already asserts
// no segment exceeds, rather than inventing a second constant.
func New(rdb *redis.Client, cfg config.Constants, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = cfg.MaxSegmentSpan()
	}
	return &Store{rdb: rdb, cfg: cfg, ttl: ttl}
}

func stateKey(sessionID string) string { return stateKeyPrefix + sessionID }

// Load fetches a session's persisted state, or a fresh Accumulator if none is
// found. `existed` is true iff a key was actually read — false covers both "a
// genuinely new session" and "this id's key already hit its fixed TTL
// deadline," which are indistinguishable by design (Redis retains no memory
// of an expired key); a late event in the second case is the documented
// reconcile-or-drop case, not something this layer can silently repair.
func (s *Store) Load(ctx context.Context, sessionID string, version uint64) (acc *segments.Accumulator, existed bool, err error) {
	raw, err := s.rdb.Get(ctx, stateKey(sessionID)).Bytes()
	if err == redis.Nil {
		return segments.NewAccumulator(s.cfg, version), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("redis get %s: %w", sessionID, err)
	}
	var st segments.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, false, fmt.Errorf("unmarshal state %s: %w", sessionID, err)
	}
	return segments.Restore(s.cfg, st), true, nil
}

// Save persists the Accumulator's snapshot and updates the active-session set
// membership. `existed` (from the paired Load call) decides the TTL behavior:
// a brand-new key gets the fixed lifetime; an existing key is updated via
// KEEPTTL so its ORIGINAL deadline is never pushed out — this is what makes
// the TTL non-refreshing/fixed rather than sliding (see package doc for why).
//
// Closed sessions are NOT deleted — their (closed) state is persisted like any
// other, as a tombstone bounded by the same fixed deadline. This is essential,
// not optional: if a closed session's key were deleted and a later event for
// that same video_session_id arrived (which happens in real data — e.g. a
// session_id can see a VideoSessionEnd followed, seconds later, by further
// playback events under the same id), Load() would find no key and hand back
// a brand-new, non-closed Accumulator — silently resurrecting a session R5
// says must stay dead, and fabricating segments that don't exist in the batch
// build. Keeping the tombstone means Load() restores an Accumulator whose
// Closed() is true, so Apply() correctly no-ops on it (batch's in-memory
// `closed` flag never "forgets" for the same reason — it's one continuous
// pass over the session's full event list; the tombstone is what gives the
// streaming path the same property across separate Load/Apply/Save round
// trips). Caught by comparing streamd's real-Redis output against the batch
// builder on an exported slice of production-shaped raw_events — see
// cmd/validateredis.
func (s *Store) Save(ctx context.Context, sessionID string, acc *segments.Accumulator, now time.Time, existed bool) error {
	snap := acc.Snapshot()
	buf, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal state %s: %w", sessionID, err)
	}

	pipe := s.rdb.TxPipeline()
	if existed {
		pipe.Set(ctx, stateKey(sessionID), buf, redis.KeepTTL)
	} else {
		pipe.Set(ctx, stateKey(sessionID), buf, s.ttl)
	}
	if acc.Active(now) {
		pipe.SAdd(ctx, activeSetKey, sessionID)
	} else {
		pipe.SRem(ctx, activeSetKey, sessionID)
	}
	_, err = pipe.Exec(ctx)
	return err
}

// ActiveCount returns the live concurrency: the size of the active-session
// set. O(1) — this is the whole point of maintaining the set incrementally
// rather than scanning all session keys per query. Call Sweep() first for a
// wall-clock-accurate read (see Sweep doc).
func (s *Store) ActiveCount(ctx context.Context) (int64, error) {
	return s.rdb.SCard(ctx, activeSetKey).Result()
}

// ActiveSessions returns the ids of currently-active sessions (for dimension
// breakdowns — caller resolves platform/country by re-reading each session's
// state, or from a separate lightweight index; kept simple here).
func (s *Store) ActiveSessions(ctx context.Context) ([]string, error) {
	return s.rdb.SMembers(ctx, activeSetKey).Result()
}

// Sweep evicts sessions whose last event predates `now - grace` from the
// active set WITHOUT deleting their Redis state (state stays for the fixed
// TTL lateness window; only "active" status changes). This is the periodic
// heartbeat-gap check for sessions that simply stopped sending events —
// no VideoSessionEnd, no AppBackgrounded, just silence — and it is REQUIRED
// before a wall-clock "active now" read to be accurate: the active set is
// otherwise only ever touched by writes, so a silent session lingers as
// falsely-active until its own next event happens to trigger the gap check.
// (This gap was caught by cmd/validateredis comparing Redis's live count
// against an in-memory reference on real data — every mismatch was the active
// set running slightly stale between events, never a segment-level error.)
// Run this on a short interval (e.g. every few seconds) alongside streamd, or
// immediately before serving a live-count query.
//
// Cost is O(active sessions), which is exactly the live population — not
// O(total sessions) or O(history) — so it stays cheap as history grows.
func (s *Store) Sweep(ctx context.Context, now time.Time) (evicted int, err error) {
	ids, err := s.ActiveSessions(ctx)
	if err != nil {
		return 0, err
	}
	grace := s.cfg.HeartbeatGrace()
	for _, id := range ids {
		acc, _, err := s.Load(ctx, id, 0)
		if err != nil {
			continue
		}
		if now.Sub(acc.LastEventTime()) > grace {
			if err := s.rdb.SRem(ctx, activeSetKey, id).Err(); err == nil {
				evicted++
			}
		}
	}
	return evicted, nil
}

// SweepIfDue runs Sweep only if minInterval has elapsed since the last sweep
// (tracked in-process), so a hot API endpoint can call this on every request
// without turning each request into an O(active sessions) Redis scan. Safe
// for concurrent callers (e.g. concurrent HTTP requests on one Server).
func (s *Store) SweepIfDue(ctx context.Context, now time.Time, minInterval time.Duration) (evicted int, err error) {
	s.sweepMu.Lock()
	due := now.Sub(s.lastSweep) >= minInterval
	if due {
		s.lastSweep = now
	}
	s.sweepMu.Unlock()
	if !due {
		return 0, nil
	}
	return s.Sweep(ctx, now)
}

// StateAge returns how long ago a session's Redis key was first created (its
// fixed TTL deadline minus its current remaining TTL), and whether the key
// currently exists — used to decide the >TTL reconcile-or-drop path.
func (s *Store) StateAge(ctx context.Context, sessionID string, now time.Time) (age time.Duration, exists bool, err error) {
	ttl, err := s.rdb.TTL(ctx, stateKey(sessionID)).Result()
	if err != nil {
		return 0, false, err
	}
	if ttl < 0 { // -2 (no key) or -1 (no TTL, shouldn't happen)
		return 0, false, nil
	}
	return s.ttl - ttl, true, nil
}

// ApplyEvent is the per-event unit of work the streaming consumer calls:
// load → apply one event → save, returning any segments finalized as a side
// effect.
func (s *Store) ApplyEvent(ctx context.Context, e models.RawEvent, version uint64) (closed []models.Segment, err error) {
	acc, existed, err := s.Load(ctx, e.VideoSessionID, version)
	if err != nil {
		return nil, err
	}
	closed = acc.Apply(e)
	if err := s.Save(ctx, e.VideoSessionID, acc, e.EventTimestamp, existed); err != nil {
		return closed, err
	}
	return closed, nil
}

// ApplyEventAndSnapshot is ApplyEvent plus the session's current in-progress
// segment (see Accumulator.OpenSnapshot) — for a caller that wants to write a
// "still active" row on every event rather than wait for the segment to
// close. The snapshot's version is the event's own timestamp (seconds), not
// the accumulator's session-scoped version: it must increase monotonically
// across successive open snapshots for the SAME segment (guaranteed, since
// events are processed in time order) and stay below the eventual close's
// version so the real close always wins under FINAL.
//
// ponytail: relies on the close version (caller-supplied, typically
// streamd's process-start wall clock) staying above any live event
// timestamp — true for a historical replay (this dataset) or a streamd run
// shorter than "now minus event time." A streamd that runs for days against
// truly-live events would need per-event-timestamp versioning on the close
// path too; not needed for this dataset.
func (s *Store) ApplyEventAndSnapshot(ctx context.Context, e models.RawEvent, version uint64) (closed []models.Segment, open *models.Segment, err error) {
	acc, existed, err := s.Load(ctx, e.VideoSessionID, version)
	if err != nil {
		return nil, nil, err
	}
	closed = acc.Apply(e)
	if err := s.Save(ctx, e.VideoSessionID, acc, e.EventTimestamp, existed); err != nil {
		return closed, nil, err
	}
	if seg, ok := acc.OpenSnapshot(uint64(e.EventTimestamp.Unix())); ok {
		open = &seg
	}
	return closed, open, nil
}
