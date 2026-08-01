package segments

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/models"
)

// TestStreamingMatchesBatch is the correctness proof for the Redis/streaming path:
// feeding events one-at-a-time through per-session Accumulators (interleaved across
// sessions, in global event-time order) must produce byte-identical segments to the
// batch builder. If they match, the streaming path cannot "miss" — it's the same
// state machine, just fed incrementally.
func TestStreamingMatchesBatch(t *testing.T) {
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	ev := func(sid string, sec int, et, e string) models.RawEvent {
		return models.RawEvent{VideoSessionID: sid, EventType: et, Event: e, EventTimestamp: at(sec), Platform: "ANDROID_PHONE", Country: "india"}
	}

	events := []models.RawEvent{
		// S1: play, pause→resume (split), background→foreground+play (split), close
		ev("S1", 0, "VideoSessionStart", "VideoSessionStart"),
		ev("S1", 0, "VideoPlay", "VideoPlay"),
		ev("S1", 30, "VideoHeartbeat", "buffer-health"),
		ev("S1", 60, "VideoHeartbeat", "pause"),
		ev("S1", 90, "VideoHeartbeat", "resume"),
		ev("S1", 120, "VideoHeartbeat", "buffer-health"),
		ev("S1", 150, "AppBackgrounded", "AppBackgrounded"),
		ev("S1", 200, "AppForegrounded", "AppForegrounded"),
		ev("S1", 205, "VideoPlay", "VideoPlay"),
		ev("S1", 260, "VideoSessionEnd", "VideoSessionEnd"),
		// S2: play, heartbeat, >90s gap (split), heartbeat, close
		ev("S2", 10, "VideoSessionStart", "VideoSessionStart"),
		ev("S2", 10, "VideoPlay", "VideoPlay"),
		ev("S2", 40, "VideoHeartbeat", "network-activity"),
		ev("S2", 200, "VideoHeartbeat", "network-activity"), // 160s gap → close prev at 40+90
		ev("S2", 230, "VideoSessionEnd", "VideoSessionEnd"),
		// S3: play, BufferStart/End (D3 active → one segment), close
		ev("S3", 5, "VideoSessionStart", "VideoSessionStart"),
		ev("S3", 5, "VideoPlay", "VideoPlay"),
		ev("S3", 35, "VideoHeartbeat", "BufferStart"),
		ev("S3", 45, "VideoHeartbeat", "BufferEnd"),
		ev("S3", 70, "VideoHeartbeat", "buffer-health"),
		ev("S3", 100, "VideoSessionEnd", "VideoSessionEnd"),
	}
	cfg := config.DefaultConstants()
	wm := at(300)

	// Batch
	batch := NewBuilder(cfg, 1).BuildAll(events, wm)

	// Streaming: global event-time order, per-session accumulators, one event at a time.
	streamEvents := make([]models.RawEvent, len(events))
	copy(streamEvents, events)
	sort.SliceStable(streamEvents, func(i, j int) bool { return eventLess(streamEvents[i], streamEvents[j]) })
	accs := map[string]*Accumulator{}
	var stream []models.Segment
	for _, e := range streamEvents {
		a := accs[e.VideoSessionID]
		if a == nil {
			a = NewAccumulator(cfg, 1)
			accs[e.VideoSessionID] = a
		}
		for _, s := range a.Apply(e) {
			s.VideoSessionID = e.VideoSessionID
			stream = append(stream, s)
		}
	}
	for sid, a := range accs {
		for _, s := range a.Finalize(wm) {
			s.VideoSessionID = sid
			stream = append(stream, s)
		}
	}

	key := func(segs []models.Segment) []string {
		out := make([]string, len(segs))
		for i, s := range segs {
			out[i] = fmt.Sprintf("%s|%d|%s|%s|%s", s.VideoSessionID, s.SegmentID,
				s.SegmentStart.UTC().Format(time.RFC3339), s.SegmentEnd.UTC().Format(time.RFC3339), s.CloseReason)
		}
		sort.Strings(out)
		return out
	}

	assert.NotEmpty(t, batch)
	assert.Equal(t, key(batch), key(stream), "streaming segments must equal batch segments")
	// Expect S1 to split into 3 (pause + background), S2 into 2 (gap), S3 stays 1 (buffer active).
	assert.Equal(t, 6, len(batch), "expected 3+2+1 segments")
}

// TestActivePredicate checks the live-count predicate against grace/state.
func TestActivePredicate(t *testing.T) {
	base := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	cfg := config.DefaultConstants()
	a := NewAccumulator(cfg, 1)
	a.Apply(models.RawEvent{VideoSessionID: "X", EventType: "VideoPlay", Event: "VideoPlay", EventTimestamp: at(0)})
	assert.True(t, a.Active(at(10)), "active shortly after play")
	assert.True(t, a.Active(at(90)), "active within 90s grace")
	assert.False(t, a.Active(at(91)), "inactive past grace (heartbeat gap)")
	a.Apply(models.RawEvent{VideoSessionID: "X", EventType: "VideoHeartbeat", Event: "pause", EventTimestamp: at(30)})
	assert.False(t, a.Active(at(31)), "inactive after pause")
}
