package segments_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
	"github.com/prathmeshxdev/pulse/internal/segments"
)

func ts(h, m, s int) time.Time {
	return time.Date(2026, 1, 15, h, m, s, 0, time.UTC)
}

func tsMs(h, m, s, ms int) time.Time {
	return time.Date(2026, 1, 15, h, m, s, ms*1_000_000, time.UTC)
}

func ev(session string, t time.Time, eventType, event string) models.RawEvent {
	return models.RawEvent{
		VideoSessionID:   session,
		UserID:           "u1",
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

// withKeepalives inserts VideoHeartbeat rows every 30s in (start, end) so R4
// does not fire on fixtures that intentionally span longer than the grace window.
func withKeepalives(session string, start, end time.Time, base []models.RawEvent) []models.RawEvent {
	out := make([]models.RawEvent, 0, len(base)+32)
	out = append(out, base...)
	for t := start.Add(30 * time.Second); t.Before(end); t = t.Add(30 * time.Second) {
		out = append(out, ev(session, t, "VideoHeartbeat", "keepalive"))
	}
	return out
}

func build(t *testing.T, events []models.RawEvent, watermark time.Time) []models.Segment {
	t.Helper()
	b := segments.NewBuilder(config.DefaultConstants(), 1)
	return b.BuildSession(events[0].VideoSessionID, events, watermark)
}

func TestFixture01_CleanPlayEnd(t *testing.T) {
	start, end := ts(10, 0, 0), ts(10, 5, 0)
	events := withKeepalives("s1", start, end, []models.RawEvent{
		ev("s1", start, "VideoSessionStart", ""),
		ev("s1", start, "VideoPlay", ""),
		ev("s1", end, "VideoSessionEnd", ""),
	})
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	assert.Equal(t, start, segs[0].SegmentStart)
	assert.Equal(t, end, segs[0].SegmentEnd)
	assert.Equal(t, models.CloseReasonSessionEnd, segs[0].CloseReason)
	assert.Equal(t, uint8(1), segs[0].IsFinal)
}

func TestFixture02_PauseResume(t *testing.T) {
	// R1: pause closes; resume opens; keepalives during pause must not resurrect.
	events := []models.RawEvent{
		ev("s2", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s2", ts(10, 0, 0), "VideoPlay", ""),
	}
	events = withKeepalives("s2", ts(10, 0, 0), ts(10, 5, 0), events)
	events = append(events,
		ev("s2", ts(10, 5, 0), "VideoHeartbeat", "pause"),
		ev("s2", ts(10, 5, 10), "VideoHeartbeat", "keepalive"), // must NOT resurrect
		ev("s2", ts(10, 5, 21), "VideoHeartbeat", "resume"),
	)
	events = withKeepalives("s2", ts(10, 5, 21), ts(10, 8, 0), events)
	events = append(events, ev("s2", ts(10, 8, 0), "VideoSessionEnd", ""))

	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 2)
	assert.Equal(t, models.CloseReasonPause, segs[0].CloseReason)
	assert.Equal(t, ts(10, 5, 0), segs[0].SegmentEnd)
	assert.Equal(t, ts(10, 5, 21), segs[1].SegmentStart)
	assert.Equal(t, ts(10, 8, 0), segs[1].SegmentEnd)

	d := deltas.EmitAll(segs)
	ids := map[uint64]struct{}{}
	for _, s := range segs {
		ids[s.SegmentID] = struct{}{}
	}
	curve, peak, _ := deltas.ConcurrencyCurve(d, ids, ts(10, 0, 0), ts(10, 9, 0), 72)
	assert.Equal(t, 1.0, peak)
	byMin := map[time.Time]float64{}
	for _, p := range curve {
		byMin[p.Minute] = p.Concurrency
	}
	assert.Equal(t, 1.0, byMin[ts(10, 4, 0)])
	assert.Equal(t, 1.0, byMin[ts(10, 5, 0)])
	assert.Equal(t, 1.0, byMin[ts(10, 7, 0)])
	assert.Equal(t, 0.0, byMin[ts(10, 8, 0)])
}

func TestFixture03_BufferingStaysActive(t *testing.T) {
	events := []models.RawEvent{
		ev("s3", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s3", ts(10, 0, 0), "VideoPlay", ""),
		ev("s3", ts(10, 1, 0), "VideoHeartbeat", "BufferStart"),
		ev("s3", ts(10, 1, 30), "VideoHeartbeat", "BufferEnd"),
		ev("s3", ts(10, 2, 0), "VideoHeartbeat", "keepalive"),
		ev("s3", ts(10, 3, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	assert.Equal(t, ts(10, 0, 0), segs[0].SegmentStart)
	assert.Equal(t, ts(10, 3, 0), segs[0].SegmentEnd)
}

func TestFixture04_BackgroundedHeartbeatDoesNotResurrect(t *testing.T) {
	events := []models.RawEvent{
		ev("s4", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s4", ts(10, 0, 0), "VideoPlay", ""),
		ev("s4", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
		ev("s4", ts(10, 2, 0), "AppBackgrounded", ""),
		ev("s4", ts(10, 2, 10), "VideoHeartbeat", "keepalive"), // must not resurrect
		ev("s4", ts(10, 3, 0), "AppForegrounded", ""),
		ev("s4", ts(10, 3, 5), "VideoHeartbeat", "keepalive"),
		ev("s4", ts(10, 4, 0), "VideoHeartbeat", "keepalive"),
		ev("s4", ts(10, 5, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 2)
	assert.Equal(t, models.CloseReasonBackground, segs[0].CloseReason)
	assert.Equal(t, ts(10, 2, 0), segs[0].SegmentEnd)
	assert.Equal(t, ts(10, 3, 5), segs[1].SegmentStart)
}

func TestFixture05_HeartbeatGap(t *testing.T) {
	// Intentionally sparse — R4 must cut at last_keepalive + 90s.
	events := []models.RawEvent{
		ev("s5", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s5", ts(10, 0, 0), "VideoPlay", ""),
		ev("s5", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
		ev("s5", ts(10, 6, 0), "VideoHeartbeat", "keepalive"),
		ev("s5", ts(10, 6, 30), "VideoHeartbeat", "keepalive"),
		ev("s5", ts(10, 7, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 2)
	assert.Equal(t, models.CloseReasonHeartbeat, segs[0].CloseReason)
	assert.Equal(t, ts(10, 1, 0).Add(90*time.Second), segs[0].SegmentEnd)
	assert.Equal(t, ts(10, 6, 0), segs[1].SegmentStart)
}

func TestFixture06_SubMinuteSegment(t *testing.T) {
	events := []models.RawEvent{
		ev("s6", tsMs(10, 0, 10, 0), "VideoSessionStart", ""),
		ev("s6", tsMs(10, 0, 10, 0), "VideoPlay", ""),
		ev("s6", tsMs(10, 0, 50, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	d := deltas.EmitAnyOverlap(segs[0])
	require.Len(t, d, 2)
	assert.True(t, d[1].Minute.After(d[0].Minute), "minus must be after plus")
	assert.Equal(t, ts(10, 0, 0), d[0].Minute)
	assert.Equal(t, ts(10, 1, 0), d[1].Minute)

	ids := map[uint64]struct{}{segs[0].SegmentID: {}}
	_, peak, _ := deltas.ConcurrencyCurve(d, ids, ts(10, 0, 0), ts(10, 2, 0), 72)
	assert.Equal(t, 1.0, peak)
}

func TestFixture07_CrossesHourAndDay(t *testing.T) {
	start := time.Date(2026, 1, 15, 23, 50, 0, 0, time.UTC)
	end := time.Date(2026, 1, 16, 0, 10, 0, 0, time.UTC)
	events := withKeepalives("s7", start, end, []models.RawEvent{
		ev("s7", start, "VideoSessionStart", ""),
		ev("s7", start, "VideoPlay", ""),
		ev("s7", end, "VideoSessionEnd", ""),
	})
	segs := build(t, events, end.Add(time.Hour))
	require.Len(t, segs, 1)
	assert.Equal(t, start, segs[0].SegmentStart)
	assert.Equal(t, end, segs[0].SegmentEnd)
}

func TestFixture08_EndWhilePausedOrBackgrounded(t *testing.T) {
	t.Run("end while paused", func(t *testing.T) {
		events := []models.RawEvent{
			ev("s8a", ts(10, 0, 0), "VideoSessionStart", ""),
			ev("s8a", ts(10, 0, 0), "VideoPlay", ""),
			ev("s8a", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
			ev("s8a", ts(10, 2, 0), "VideoHeartbeat", "pause"),
			ev("s8a", ts(10, 2, 30), "VideoHeartbeat", "keepalive"),
			ev("s8a", ts(10, 3, 0), "VideoSessionEnd", ""),
		}
		segs := build(t, events, ts(12, 0, 0))
		require.Len(t, segs, 1)
		assert.Equal(t, models.CloseReasonPause, segs[0].CloseReason)
		assert.Equal(t, ts(10, 2, 0), segs[0].SegmentEnd)
	})
	t.Run("end while backgrounded", func(t *testing.T) {
		events := []models.RawEvent{
			ev("s8b", ts(10, 0, 0), "VideoSessionStart", ""),
			ev("s8b", ts(10, 0, 0), "VideoPlay", ""),
			ev("s8b", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
			ev("s8b", ts(10, 2, 0), "AppBackgrounded", ""),
			ev("s8b", ts(10, 3, 0), "VideoSessionEnd", ""),
		}
		segs := build(t, events, ts(12, 0, 0))
		require.Len(t, segs, 1)
		assert.Equal(t, models.CloseReasonBackground, segs[0].CloseReason)
	})
}

func TestFixture09_UnmatchedResume(t *testing.T) {
	events := []models.RawEvent{
		ev("s9", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s9", ts(10, 0, 0), "VideoPlay", ""),
		ev("s9", ts(10, 1, 0), "VideoHeartbeat", "resume"),
		ev("s9", ts(10, 1, 30), "VideoHeartbeat", "keepalive"),
		ev("s9", ts(10, 2, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	assert.Equal(t, ts(10, 0, 0), segs[0].SegmentStart)
	assert.Equal(t, ts(10, 2, 0), segs[0].SegmentEnd)
}

func TestFixture10_VideoErrorThenPlay(t *testing.T) {
	events := []models.RawEvent{
		ev("s10", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s10", ts(10, 0, 0), "VideoPlay", ""),
		ev("s10", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
		ev("s10", ts(10, 2, 0), "VideoError", ""),
		ev("s10", ts(10, 3, 0), "VideoPlay", ""),
		ev("s10", ts(10, 4, 0), "VideoHeartbeat", "keepalive"),
		ev("s10", ts(10, 5, 0), "VideoSessionEnd", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 2)
	assert.Equal(t, models.CloseReasonError, segs[0].CloseReason)
	assert.Equal(t, ts(10, 3, 0), segs[1].SegmentStart)
	assert.Equal(t, uint8(1), segs[1].IsFinal)
}

func TestFixture11_EventsAfterCloseIgnored(t *testing.T) {
	events := []models.RawEvent{
		ev("s11", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s11", ts(10, 0, 0), "VideoPlay", ""),
		ev("s11", ts(10, 1, 0), "VideoHeartbeat", "keepalive"),
		ev("s11", ts(10, 2, 0), "VideoSessionEnd", ""),
		ev("s11", ts(10, 3, 0), "VideoPlay", ""),
		ev("s11", ts(10, 4, 0), "VideoHeartbeat", "keepalive"),
		ev("s11", ts(10, 5, 0), "VideoSessionStart", ""),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	assert.Equal(t, ts(10, 2, 0), segs[0].SegmentEnd)
}

func TestFixture12_TwoSessionsOneUser(t *testing.T) {
	b := segments.NewBuilder(config.DefaultConstants(), 1)
	mk := func(sid string, start, end time.Time) []models.Segment {
		events := withKeepalives(sid, start, end, []models.RawEvent{
			ev(sid, start, "VideoSessionStart", ""),
			ev(sid, start, "VideoPlay", ""),
			ev(sid, end, "VideoSessionEnd", ""),
		})
		for i := range events {
			events[i].UserID = "same-user"
		}
		return b.BuildSession(sid, events, ts(12, 0, 0))
	}
	all := append(
		mk("sa", ts(10, 0, 0), ts(10, 10, 0)),
		mk("sb", ts(10, 5, 0), ts(10, 15, 0))...,
	)
	d := deltas.EmitAll(all)
	_, peak, _ := deltas.ConcurrencyCurve(d, nil, ts(10, 0, 0), ts(10, 16, 0), 72)
	assert.Equal(t, 2.0, peak)
}

func TestFixture13_WatermarkClamp(t *testing.T) {
	wm := ts(10, 5, 0)
	events := withKeepalives("s13", ts(10, 0, 0), wm, []models.RawEvent{
		ev("s13", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("s13", ts(10, 0, 0), "VideoPlay", ""),
		ev("s13", ts(10, 4, 30), "VideoHeartbeat", "keepalive"),
	})
	segs := build(t, events, wm)
	require.Len(t, segs, 1)
	assert.Equal(t, models.CloseReasonWatermark, segs[0].CloseReason)
	assert.Equal(t, wm, segs[0].SegmentEnd)
	assert.True(t, !segs[0].SegmentEnd.After(wm))
}

func TestFixture14_ThreeSessionsSameMinute(t *testing.T) {
	b := segments.NewBuilder(config.DefaultConstants(), 1)
	var all []models.Segment
	for _, sid := range []string{"x", "y", "z"} {
		events := withKeepalives(sid, ts(10, 5, 0), ts(10, 20, 0), []models.RawEvent{
			ev(sid, ts(10, 5, 0), "VideoSessionStart", ""),
			ev(sid, ts(10, 5, 0), "VideoPlay", ""),
			ev(sid, ts(10, 20, 0), "VideoSessionEnd", ""),
		})
		all = append(all, b.BuildSession(sid, events, ts(12, 0, 0))...)
	}
	d := deltas.EmitAll(all)
	_, peak, _ := deltas.ConcurrencyCurve(d, nil, ts(10, 5, 0), ts(10, 21, 0), 72)
	assert.Equal(t, 3.0, peak)
}

func TestOpeningBalancePreventsNegative(t *testing.T) {
	events := withKeepalives("ob", ts(9, 0, 0), ts(10, 30, 0), []models.RawEvent{
		ev("ob", ts(9, 0, 0), "VideoSessionStart", ""),
		ev("ob", ts(9, 0, 0), "VideoPlay", ""),
		ev("ob", ts(10, 30, 0), "VideoSessionEnd", ""),
	})
	segs := build(t, events, ts(12, 0, 0))
	d := deltas.EmitAll(segs)
	curve, peak, avg := deltas.ConcurrencyCurve(d, nil, ts(10, 0, 0), ts(11, 0, 0), 72)
	for _, p := range curve {
		assert.GreaterOrEqual(t, p.Concurrency, 0.0, "minute %s", p.Minute)
	}
	assert.Equal(t, 1.0, peak)
	assert.InDelta(t, 0.5, avg, 0.02)
}

func TestDenseGridAverage(t *testing.T) {
	events := withKeepalives("dg", ts(10, 0, 0), ts(10, 10, 0), []models.RawEvent{
		ev("dg", ts(10, 0, 0), "VideoSessionStart", ""),
		ev("dg", ts(10, 0, 0), "VideoPlay", ""),
		ev("dg", ts(10, 10, 0), "VideoSessionEnd", ""),
	})
	segs := build(t, events, ts(12, 0, 0))
	d := deltas.EmitAll(segs)
	_, _, avg := deltas.ConcurrencyCurve(d, nil, ts(10, 0, 0), ts(10, 20, 0), 72)
	assert.InDelta(t, 0.5, avg, 0.001)
}

func TestDimensionSnapshotAtSegmentStart(t *testing.T) {
	e0 := ev("dim", ts(10, 0, 0), "VideoSessionStart", "")
	e1 := ev("dim", ts(10, 0, 0), "VideoPlay", "")
	e1.SubtitleLanguage = "unk"
	e2 := ev("dim", ts(10, 1, 0), "VideoHeartbeat", "keepalive")
	e2.SubtitleLanguage = "OFF"
	e3 := ev("dim", ts(10, 2, 0), "VideoSessionEnd", "")
	e3.SubtitleLanguage = "eng"
	segs := build(t, []models.RawEvent{e0, e1, e2, e3}, ts(12, 0, 0))
	require.Len(t, segs, 1)
	assert.Equal(t, "unk", segs[0].SubtitleLanguage)
}

func TestNoCollapsingDeltas(t *testing.T) {
	events := []models.RawEvent{
		ev("nc", tsMs(10, 0, 10, 0), "VideoSessionStart", ""),
		ev("nc", tsMs(10, 0, 10, 0), "VideoPlay", ""),
		ev("nc", tsMs(10, 0, 40, 0), "VideoHeartbeat", "pause"),
	}
	segs := build(t, events, ts(12, 0, 0))
	require.Len(t, segs, 1)
	d := deltas.EmitAnyOverlap(segs[0])
	require.Len(t, d, 2)
	assert.True(t, d[1].Minute.After(d[0].Minute))
}
