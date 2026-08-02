package users_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
	"github.com/prathmeshxdev/pulse/internal/users"
)

func ts(h, m, s int) time.Time {
	return time.Date(2026, 1, 15, h, m, s, 0, time.UTC)
}

func ev(session, user string, t time.Time, eventType, event string) models.RawEvent {
	return models.RawEvent{
		VideoSessionID:   session,
		UserID:           user,
		ContentID:        42,
		EventType:        eventType,
		Event:            event,
		EventTimestamp:   t,
		Platform:         "ANDROID",
		Country:          "india",
		AppVersion:       "1.0",
		AudioLanguage:    "hin",
		SubtitleLanguage: "OFF",
		PlayerVersion:    "p1",
	}
}

func withKeepalives(session string, start, end time.Time, base []models.RawEvent) []models.RawEvent {
	out := append([]models.RawEvent{}, base...)
	for t := start.Add(30 * time.Second); t.Before(end); t = t.Add(30 * time.Second) {
		out = append(out, ev(session, base[0].UserID, t, "VideoHeartbeat", "keepalive"))
	}
	return out
}

// Evidence fixture: one user, two overlapping sessions → session peak 2, user peak 1.
func TestFixture_TwoSessionsOneUser(t *testing.T) {
	const user = "u-overlap"
	wm := ts(12, 0, 0)
	start1, end1 := ts(10, 0, 0), ts(10, 30, 0)
	start2, end2 := ts(10, 10, 0), ts(10, 20, 0)

	events1 := withKeepalives("s1", start1, end1, []models.RawEvent{
		ev("s1", user, start1, "VideoSessionStart", ""),
		ev("s1", user, start1, "VideoPlay", ""),
		ev("s1", user, end1, "VideoSessionEnd", ""),
	})
	events2 := withKeepalives("s2", start2, end2, []models.RawEvent{
		ev("s2", user, start2, "VideoSessionStart", ""),
		ev("s2", user, start2, "VideoPlay", ""),
		ev("s2", user, end2, "VideoSessionEnd", ""),
	})

	b := segments.NewBuilder(config.DefaultConstants(), 1)
	segs := append(b.BuildSession("s1", events1, wm), b.BuildSession("s2", events2, wm)...)
	require.Len(t, segs, 2)

	userSegs := users.MergeIslands(segs, 1)
	require.Len(t, userSegs, 1, "overlapping sessions merge to one user island")
	assert.Equal(t, start1, userSegs[0].SegmentStart)
	assert.Equal(t, end1, userSegs[0].SegmentEnd)

	rs, re := ts(10, 0, 0), ts(11, 0, 0)
	sessionDeltas := deltas.EmitAll(segs)
	userDeltas := users.EmitClosedDeltas(userSegs)

	_, sessionPeak, _ := deltas.ConcurrencyCurve(sessionDeltas, nil, rs, re, 72)
	userPeak, _ := users.ConcurrencyCurve(userDeltas, nil, rs, re, 72)

	assert.Equal(t, float64(2), sessionPeak, "session-aware peak at overlap")
	assert.Equal(t, float64(1), userPeak, "session-independent (user) peak")
}

func TestMergeIslands_AdjacentSessionsDoNotMerge(t *testing.T) {
	wm := ts(12, 0, 0)
	user := "u-adj"
	e1 := withKeepalives("s1", ts(10, 0, 0), ts(10, 10, 0), []models.RawEvent{
		ev("s1", user, ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s1", user, ts(10, 0, 0), "VideoPlay", ""),
		ev("s1", user, ts(10, 10, 0), "VideoSessionEnd", ""),
	})
	e2 := withKeepalives("s2", ts(10, 10, 0), ts(10, 20, 0), []models.RawEvent{
		ev("s2", user, ts(10, 10, 0), "VideoSessionStart", ""),
		ev("s2", user, ts(10, 10, 0), "VideoPlay", ""),
		ev("s2", user, ts(10, 20, 0), "VideoSessionEnd", ""),
	})
	b := segments.NewBuilder(config.DefaultConstants(), 1)
	segs := append(b.BuildSession("s1", e1, wm), b.BuildSession("s2", e2, wm)...)
	userSegs := users.MergeIslands(segs, 1)
	assert.Len(t, userSegs, 2, "back-to-back sessions are separate islands")
}
