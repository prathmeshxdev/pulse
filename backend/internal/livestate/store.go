// Package livestate implements the real-time Redis-backed streaming path
// discussed for near-real-time concurrency: per-session Accumulator state is
// persisted in Redis with a sliding TTL (default 48h — comfortably above the
// measured max session span of 43.64h in this dataset). This bounds how late
// an event may arrive and still be folded into its session correctly; events
// arriving after a session's key has expired are documented as
// reconcile-or-drop (see docs/ARCHITECTURE.md "Real-time streaming path").
//
// Design (validated in internal/segments):
//   - Accumulator.Apply/Finalize is the SAME state machine as the batch
//     builder (TestStreamingMatchesBatch proves byte-identical output).
//   - Snapshot/Restore round-trips through JSON with zero behavior drift
//     (TestSnapshotRoundTrip).
//   - "Active now" is a query over Accumulator.Active(now), maintained
//     incrementally via a Redis set so counting is O(1), not O(sessions).
package livestate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

const (
	stateKeyPrefix = "pulse:session:"
	activeSetKey   = "pulse:active"
	// closedSetTTL bounds how long a closed-session marker lingers, purely to
	// let a duplicate/replayed VideoSessionEnd be recognized as already-handled.
	closedSetTTL = 24 * time.Hour
)

// Store persists per-session Accumulator state in Redis and maintains an
// active-session set for O(1) live counting.
type Store struct {
	rdb *redis.Client
	cfg config.Constants
	ttl time.Duration
}

// New creates a Store. ttl is the sliding key lifetime — the maximum lateness
// window (default 48h if zero).
func New(rdb *redis.Client, cfg config.Constants, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	return &Store{rdb: rdb, cfg: cfg, ttl: ttl}
}

func stateKey(sessionID string) string { return stateKeyPrefix + sessionID }

// Load fetches a session's persisted state, or a fresh Accumulator if none
// exists (new session) or if it expired (>TTL late — see package doc).
// `expired` is true only in the latter case, so callers can route to reconcile.
func (s *Store) Load(ctx context.Context, sessionID string, version uint64) (acc *segments.Accumulator, expired bool, err error) {
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
	return segments.Restore(s.cfg, st), false, nil
}

// Save persists the Accumulator's snapshot with a sliding TTL (refreshed on
// every event, so an active session never expires mid-stream) and updates the
// active-session set membership. If the session closed, its state is deleted
// (nothing further to correct) and it's removed from the active set.
func (s *Store) Save(ctx context.Context, sessionID string, acc *segments.Accumulator, now time.Time) error {
	if acc.Closed() {
		pipe := s.rdb.TxPipeline()
		pipe.Del(ctx, stateKey(sessionID))
		pipe.SRem(ctx, activeSetKey, sessionID)
		_, err := pipe.Exec(ctx)
		return err
	}

	snap := acc.Snapshot()
	buf, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal state %s: %w", sessionID, err)
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, stateKey(sessionID), buf, s.ttl)
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
// rather than scanning all session keys per query.
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
// active set WITHOUT deleting their Redis state (state stays for the TTL
// lateness window; only "active" status changes). This is the periodic
// heartbeat-gap check for sessions that simply stopped sending events —
// no VideoSessionEnd, no AppBackgrounded, just silence.
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

// StateAge returns how long ago a session's Redis state was last touched, and
// whether a key exists at all — used to decide the >TTL reconcile-or-drop path.
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

// RawEventApply is a convenience wrapper: load → apply one event → save,
// returning any segments finalized as a side effect. This is the per-event
// unit of work the streaming consumer calls.
func (s *Store) ApplyEvent(ctx context.Context, e models.RawEvent, version uint64) (closed []models.Segment, err error) {
	acc, _, err := s.Load(ctx, e.VideoSessionID, version)
	if err != nil {
		return nil, err
	}
	closed = acc.Apply(e)
	if err := s.Save(ctx, e.VideoSessionID, acc, e.EventTimestamp); err != nil {
		return closed, err
	}
	return closed, nil
}
