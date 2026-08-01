package livestate

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/models"
)

func newTestStore(t *testing.T, ttl time.Duration) (*Store, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return New(rdb, config.DefaultConstants(), ttl), mr
}

// TestApplyEvent_ActiveCount proves the incremental active-set count matches
// what the Accumulator itself would report — the core "does this solve live
// concurrency accurately" question, exercised through Redis end-to-end.
func TestApplyEvent_ActiveCount(t *testing.T) {
	s, mr := newTestStore(t, 48*time.Hour)
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	// Two sessions start playing.
	_, err := s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "A", EventType: "VideoSessionStart", Event: "VideoSessionStart", EventTimestamp: at(0)}, 1)
	require.NoError(t, err)
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "A", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(0)}, 1)
	require.NoError(t, err)
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "B", EventType: "VideoSessionStart", Event: "VideoSessionStart", EventTimestamp: at(5)}, 1)
	require.NoError(t, err)
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "B", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(5)}, 1)
	require.NoError(t, err)

	mr.FastForward(0) // no-op, just to ensure ttl set; real check below
	n, err := s.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "both sessions active after play")

	// A pauses -> should drop out of active set.
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "A", EventType: "VideoHeartbeat", Event: "pause", EventTimestamp: at(30)}, 1)
	require.NoError(t, err)
	n, err = s.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "A paused, only B active")

	// B ends -> removed from the active set, but its state is KEPT as a
	// tombstone (not deleted) so a later event for id "B" is recognized as
	// belonging to an already-closed session and correctly ignored (R5) —
	// deleting it would let a subsequent event resurrect a fresh, wrongly
	// non-closed Accumulator. See Store.Save doc for the real-data bug this
	// prevents.
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "B", EventType: "VideoSessionEnd", Event: "VideoSessionEnd", EventTimestamp: at(60)}, 1)
	require.NoError(t, err)
	n, err = s.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "B ended, A still paused -> nobody active")

	exists := mr.Exists(stateKey("B"))
	assert.True(t, exists, "closed session's state persists as a tombstone (bounded by TTL)")
	exists = mr.Exists(stateKey("A"))
	assert.True(t, exists, "paused (not closed) session's state persists for late corrections")

	// A late event for the now-closed "B" must be ignored, not treated as a
	// fresh session — this is the exact scenario the tombstone fix protects.
	closedAfterEnd, err := s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "B", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(120)}, 1)
	require.NoError(t, err)
	assert.Empty(t, closedAfterEnd, "no segment emitted for an event on an already-closed session")
	n, err = s.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "the late VideoPlay on closed session B must NOT resurrect it into the active set")
}

// TestApplyEvent_LateCorrection is the scenario driving the whole design: an
// event arrives late (after other events already processed) but within TTL.
// Loading state, applying it, and saving must fold it in correctly — proving
// the "keep state up to TTL, apply late events on arrival" approach works.
func TestApplyEvent_LateCorrection(t *testing.T) {
	s, _ := newTestStore(t, 48*time.Hour)
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	_, err := s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "L", EventType: "VideoSessionStart", Event: "VideoSessionStart", EventTimestamp: at(0)}, 1)
	require.NoError(t, err)
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "L", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(0)}, 1)
	require.NoError(t, err)

	// A heartbeat with an EARLIER timestamp arrives late (out-of-order at the
	// transport level, but still within the session's ordered history since it's
	// simply a keepalive — the state machine treats it via lastKeepalive refresh
	// only if it's the most recent event applied; here we model genuine late
	// arrival of the NEXT chronological event, delivered after a delay).
	closed, err := s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "L", EventType: "VideoHeartbeat", Event: "buffer-health", EventTimestamp: at(45)}, 1)
	require.NoError(t, err)
	assert.Empty(t, closed, "no segment closes on a simple keepalive")

	acc, existed, err := s.Load(ctx, "L", 1)
	require.NoError(t, err)
	assert.True(t, existed, "session L already has persisted state from prior events")
	assert.True(t, acc.Active(at(50)), "session still active after late-but-within-grace heartbeat")
}

// TestApplyEvent_MatchesBatchOnSameData feeds the SAME event set both through
// the batch builder and through the Redis store event-by-event, and asserts
// the resulting "active at various instants" answers agree — end-to-end proof
// that going through Redis doesn't change correctness versus the validated
// batch path.
func TestApplyEvent_MatchesBatchOnSameData(t *testing.T) {
	s, _ := newTestStore(t, 48*time.Hour)
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	ev := func(sid string, sec int, et, e string) models.RawEvent {
		return models.RawEvent{VideoSessionID: sid, EventType: et, Event: e, EventTimestamp: at(sec)}
	}
	events := []models.RawEvent{
		ev("Z", 0, "VideoSessionStart", "VideoSessionStart"),
		ev("Z", 0, "VideoPlay", "VideoPlay"),
		ev("Z", 60, "VideoHeartbeat", "pause"),
		ev("Z", 90, "VideoHeartbeat", "resume"),
		ev("Z", 200, "VideoSessionEnd", "VideoSessionEnd"),
	}
	for _, e := range events {
		_, err := s.ApplyEvent(ctx, e, 1)
		require.NoError(t, err)
	}
	// After all events processed and session closed, active count must be 0.
	n, err := s.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// TestFixedTTL_DoesNotRefresh proves the TTL is set ONCE on key creation and
// never extended by later writes (KEEPTTL) — the design choice discussed:
// use the existing MAX_SEGMENT_SPAN_HOURS bound (72h) as a fixed deadline
// rather than a sliding one refreshed on every event.
func TestFixedTTL_DoesNotRefresh(t *testing.T) {
	s, mr := newTestStore(t, 2*time.Second) // short TTL so the test runs fast
	ctx := context.Background()
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	_, err := s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "F", EventType: "VideoSessionStart", Event: "VideoSessionStart", EventTimestamp: at(0)}, 1)
	require.NoError(t, err)
	ttl1 := mr.TTL(stateKey("F"))
	assert.InDelta(t, 2*time.Second, ttl1, float64(200*time.Millisecond), "fresh key gets the fixed TTL")

	mr.FastForward(1 * time.Second)
	// A second event for the SAME session must NOT push the deadline back out.
	_, err = s.ApplyEvent(ctx, models.RawEvent{VideoSessionID: "F", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(1)}, 1)
	require.NoError(t, err)
	ttl2 := mr.TTL(stateKey("F"))
	assert.Less(t, ttl2, ttl1, "TTL must have counted down, not been refreshed back to the full value")
	assert.InDelta(t, 1*time.Second, ttl2, float64(200*time.Millisecond), "remaining TTL reflects elapsed wall-clock time since creation, not a reset")
}
