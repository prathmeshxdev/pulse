package segments

import (
	"hash/fnv"
	"sort"
	"time"

	"github.com/prathmeshxdev/pulse/internal/config"
	"github.com/prathmeshxdev/pulse/internal/models"
)

// Builder turns ordered raw events into foreground-active segments.
type Builder struct {
	cfg     config.Constants
	version uint64
}

func NewBuilder(cfg config.Constants, version uint64) *Builder {
	return &Builder{cfg: cfg, version: version}
}

// BuildAll groups events by session, runs the state machine, returns all segments.
func (b *Builder) BuildAll(events []models.RawEvent, watermark time.Time) []models.Segment {
	bySession := map[string][]models.RawEvent{}
	for _, e := range events {
		bySession[e.VideoSessionID] = append(bySession[e.VideoSessionID], e)
	}
	out := make([]models.Segment, 0)
	for sid, evs := range bySession {
		out = append(out, b.BuildSession(sid, evs, watermark)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].VideoSessionID != out[j].VideoSessionID {
			return out[i].VideoSessionID < out[j].VideoSessionID
		}
		return out[i].SegmentStart.Before(out[j].SegmentStart)
	})
	return out
}

// BuildSession implements the FINAL_PLAN §1.4–§1.5 state machine for one session
// by driving the shared Accumulator — the exact same code path the streaming/Redis
// path uses, so batch and streaming produce identical segments by construction.
func (b *Builder) BuildSession(sessionID string, events []models.RawEvent, watermark time.Time) []models.Segment {
	if len(events) == 0 {
		return nil
	}
	sort.SliceStable(events, func(i, j int) bool {
		return eventLess(events[i], events[j])
	})
	acc := NewAccumulator(b.cfg, b.version)
	var segs []models.Segment
	for _, e := range events {
		segs = append(segs, acc.Apply(e)...)
	}
	segs = append(segs, acc.Finalize(watermark)...)
	for i := range segs {
		segs[i].VideoSessionID = sessionID
	}
	return segs
}

// Accumulator is the per-session incremental state machine. Batch feeds it a
// session's events in order then Finalize()s; the streaming path loads it from
// Redis, Apply()s one event, and persists it — same logic, same output.
type Accumulator struct {
	st      sessionState
	version uint64
}

// NewAccumulator creates a fresh per-session state (session_open + foreground
// default true, playing false; §1.4).
func NewAccumulator(cfg config.Constants, version uint64) *Accumulator {
	return &Accumulator{
		st: sessionState{
			sessionOpen:  true,
			foreground:   true,
			playing:      false,
			grace:        cfg.HeartbeatGrace(),
			pauseActive:  !cfg.PauseCountsAsActive,
			bufferActive: cfg.BufferingCountsActive,
		},
		version: version,
	}
}

// Apply processes one event and returns any segment(s) that closed as a result
// (zero-length segments are dropped, R7). Events after close are ignored (R5).
func (a *Accumulator) Apply(e models.RawEvent) []models.Segment {
	st := &a.st
	if st.closed {
		return nil
	}
	st.lastEventTime = e.EventTimestamp
	var out []models.Segment

	// Heartbeat gap before applying this event (R4).
	if st.inActive && !st.lastKeepalive.IsZero() {
		gapEnd := st.lastKeepalive.Add(st.grace)
		if e.EventTimestamp.After(gapEnd) {
			out = appendSeg(out, st.closeSegment(gapEnd, models.CloseReasonHeartbeat, false, a.version))
		}
	}

	switch Classify(e.EventType, e.Event) {
	case models.SignalOpen:
		st.sessionOpen = true
		st.foreground = true
		if !st.inActive {
			st.playing = false
		}

	case models.SignalPlay:
		st.playing = true
		st.lastKeepalive = e.EventTimestamp
		st.maybeOpen(e)

	case models.SignalKeepalive:
		st.keepalive(e)

	case models.SignalBufferStart:
		if st.bufferActive {
			st.keepalive(e)
		} else {
			if st.inActive {
				out = appendSeg(out, st.closeSegment(e.EventTimestamp, models.CloseReasonBuffer, false, a.version))
			}
			st.buffering = true
		}

	case models.SignalBufferEnd:
		if st.bufferActive {
			st.keepalive(e)
		} else {
			st.buffering = false
			st.maybeOpen(e)
		}

	case models.SignalPause:
		if st.pauseActive {
			if st.inActive {
				out = appendSeg(out, st.closeSegment(e.EventTimestamp, models.CloseReasonPause, false, a.version))
			}
			st.playing = false
		} else {
			st.playing = true
			st.lastKeepalive = e.EventTimestamp
			st.maybeOpen(e)
		}

	case models.SignalResume:
		if !st.playing {
			st.playing = true
			st.lastKeepalive = e.EventTimestamp
			st.maybeOpen(e)
		}

	case models.SignalBackground:
		if st.inActive {
			out = appendSeg(out, st.closeSegment(e.EventTimestamp, models.CloseReasonBackground, false, a.version))
		}
		st.foreground = false

	case models.SignalForeground:
		st.foreground = true

	case models.SignalError:
		if st.inActive {
			out = appendSeg(out, st.closeSegment(e.EventTimestamp, models.CloseReasonError, false, a.version))
		}
		st.playing = false

	case models.SignalClose:
		if st.inActive {
			out = appendSeg(out, st.closeSegment(e.EventTimestamp, models.CloseReasonSessionEnd, true, a.version))
		}
		st.sessionOpen = false
		st.closed = true
		st.playing = false

	case models.SignalIgnore:
	}
	return out
}

// Finalize emits the trailing open segment clamped to the watermark (R8).
func (a *Accumulator) Finalize(watermark time.Time) []models.Segment {
	st := &a.st
	if st.inActive && !st.closed {
		end := st.lastKeepalive.Add(st.grace)
		if !watermark.IsZero() && end.After(watermark) {
			end = watermark
		}
		return appendSeg(nil, st.closeSegment(end, models.CloseReasonWatermark, false, a.version))
	}
	return nil
}

// OpenSnapshot returns the in-progress segment as it stands right now, WITHOUT
// closing it, so a caller (streamd) can write a "still active" row to
// session_active_segments on every event instead of waiting for the segment
// to actually close — trading "freshness = next close" for "freshness =
// next event." SegmentEnd mirrors Active()/Finalize()'s own boundary
// (lastKeepalive + grace); the real close, when it happens, uses a
// later end and a higher version, so it naturally supersedes this row under
// FINAL — no new column, no query change. Returns ok=false if there's
// nothing open yet.
func (a *Accumulator) OpenSnapshot(version uint64) (models.Segment, bool) {
	st := &a.st
	if !st.inActive || st.closed {
		return models.Segment{}, false
	}
	end := st.lastKeepalive.Add(st.grace)
	if !end.After(st.segmentStart) {
		return models.Segment{}, false
	}
	return st.buildSegment(end, "", false, version), true
}

// Active reports whether the session is active at instant `at` — the live-count
// predicate: in an open segment, not ended, and heartbeat-fresh within grace.
func (a *Accumulator) Active(at time.Time) bool {
	st := a.st
	if !st.inActive || st.closed || at.Before(st.segmentStart) {
		return false
	}
	return st.lastKeepalive.IsZero() || !at.After(st.lastKeepalive.Add(st.grace))
}

// Closed reports whether the session has terminated (VideoSessionEnd).
func (a *Accumulator) Closed() bool { return a.st.closed }

// LastEventTime returns the timestamp of the most recent event applied to this
// session (regardless of signal type) — used by the streaming store to drive
// Redis's sliding TTL and to detect the >TTL "too late, fall back" case.
func (a *Accumulator) LastEventTime() time.Time { return a.st.lastEventTime }

// State is the exported, JSON-serializable snapshot of an Accumulator's
// internal state — what the Redis streaming store persists per session
// between events. Restore(cfg, version, state) reconstructs an equivalent
// Accumulator; Snapshot() extracts it. Round-tripping through State must not
// change subsequent behavior (verified by TestSnapshotRoundTrip).
type State struct {
	SessionOpen   bool            `json:"session_open"`
	Closed        bool            `json:"closed"`
	Foreground    bool            `json:"foreground"`
	Playing       bool            `json:"playing"`
	Buffering     bool            `json:"buffering"`
	InActive      bool            `json:"in_active"`
	SegmentStart  time.Time       `json:"segment_start"`
	LastKeepalive time.Time       `json:"last_keepalive"`
	LastEventTime time.Time       `json:"last_event_time"`
	Dims          models.RawEvent `json:"dims"`
	Version       uint64          `json:"version"`
}

// Snapshot extracts the current state for persistence (e.g. to Redis).
func (a *Accumulator) Snapshot() State {
	return State{
		SessionOpen:   a.st.sessionOpen,
		Closed:        a.st.closed,
		Foreground:    a.st.foreground,
		Playing:       a.st.playing,
		Buffering:     a.st.buffering,
		InActive:      a.st.inActive,
		SegmentStart:  a.st.segmentStart,
		LastKeepalive: a.st.lastKeepalive,
		LastEventTime: a.st.lastEventTime,
		Dims:          a.st.dims,
		Version:       a.version,
	}
}

// Restore rebuilds an Accumulator from a previously-persisted State plus the
// config knobs (grace/pauseActive/bufferActive are config-derived, not
// serialized, so a config change takes effect on the next Restore).
func Restore(cfg config.Constants, s State) *Accumulator {
	return &Accumulator{
		version: s.Version,
		st: sessionState{
			sessionOpen:   s.SessionOpen,
			closed:        s.Closed,
			foreground:    s.Foreground,
			playing:       s.Playing,
			buffering:     s.Buffering,
			inActive:      s.InActive,
			segmentStart:  s.SegmentStart,
			lastKeepalive: s.LastKeepalive,
			lastEventTime: s.LastEventTime,
			dims:          s.Dims,
			grace:         cfg.HeartbeatGrace(),
			pauseActive:   !cfg.PauseCountsAsActive,
			bufferActive:  cfg.BufferingCountsActive,
		},
	}
}

// appendSeg appends s only if it is non-empty (R7: drop zero-length segments).
func appendSeg(out []models.Segment, s models.Segment) []models.Segment {
	if s.SegmentEnd.After(s.SegmentStart) {
		return append(out, s)
	}
	return out
}

type sessionState struct {
	sessionOpen   bool
	closed        bool
	foreground    bool
	playing       bool
	buffering     bool // only set when BUFFERING_COUNTS_AS_ACTIVE is false (D3 flip)
	inActive      bool
	segmentStart  time.Time
	lastKeepalive time.Time
	lastEventTime time.Time // most recent event applied, any signal — TTL/staleness driver
	dims          models.RawEvent
	grace         time.Duration
	pauseActive   bool // true means pause closes (normal locked semantics)
	bufferActive  bool
}

func (st *sessionState) maybeOpen(e models.RawEvent) {
	if st.sessionOpen && st.foreground && st.playing && !st.inActive && !st.buffering {
		st.openSegment(e)
	}
}

// keepalive extends the active segment (or opens one) when all active conditions
// hold. Shared by VideoHeartbeat and, under the locked D3 default, buffer events.
func (st *sessionState) keepalive(e models.RawEvent) {
	if st.foreground && st.playing && st.sessionOpen && !st.buffering {
		if !st.inActive {
			st.openSegment(e)
		} else {
			st.lastKeepalive = e.EventTimestamp
		}
	}
}

func (st *sessionState) openSegment(e models.RawEvent) {
	st.inActive = true
	st.segmentStart = e.EventTimestamp
	st.lastKeepalive = e.EventTimestamp
	// R10: snapshot dimensions deterministically at segment start.
	st.dims = e
}

func (st *sessionState) buildSegment(end time.Time, reason string, isFinal bool, version uint64) models.Segment {
	final := uint8(0)
	if isFinal {
		final = 1
	}
	return models.Segment{
		SegmentID:        SegmentID(st.dims.VideoSessionID, st.segmentStart),
		VideoSessionID:   st.dims.VideoSessionID,
		UserID:           st.dims.UserID,
		ContentID:        st.dims.ContentID,
		Platform:         st.dims.Platform,
		Country:          st.dims.Country,
		AppVersion:       st.dims.AppVersion,
		AudioLanguage:    st.dims.AudioLanguage,
		SubtitleLanguage: st.dims.SubtitleLanguage,
		PlayerVersion:    st.dims.PlayerVersion,
		SegmentStart:     st.segmentStart,
		SegmentEnd:       end,
		IsFinal:          final,
		CloseReason:      reason,
		Version:          version,
		Properties:       cloneProperties(st.dims.Properties),
	}
}

func cloneProperties(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (st *sessionState) closeSegment(end time.Time, reason string, isFinal bool, version uint64) models.Segment {
	st.inActive = false
	return st.buildSegment(end, reason, isFinal, version)
}

func eventLess(a, b models.RawEvent) bool { return EventLess(a, b) }

// EventLess is the CANONICAL per-session event order: (event_timestamp,
// event_type, event). Same-timestamp events are common in real data (e.g.
// VideoSessionStart and VideoPlay both stamped at session start) and the
// state machine's outcome depends on which is applied first — so ANY caller
// that sorts events before feeding them to an Accumulator (batch, streamd,
// reconcile, or a validation harness) MUST use this exact order. Sorting by
// timestamp alone and leaving ties in arrival/query order was a real bug
// caught by TestStreamingMatchesBatchOnRealData: it let a same-timestamp
// event apply in a different order than the sort in BuildSession, producing
// segments that legitimately differed from batch (extra "pause" closes) on
// real production-shaped data even though every unit-test fixture passed.
func EventLess(a, b models.RawEvent) bool {
	if !a.EventTimestamp.Equal(b.EventTimestamp) {
		return a.EventTimestamp.Before(b.EventTimestamp)
	}
	if a.EventType != b.EventType {
		return a.EventType < b.EventType
	}
	return a.Event < b.Event
}

// SortEvents sorts events in place using the canonical EventLess order. Use
// this (not a bespoke timestamp-only sort) anywhere events are ordered before
// streaming/replaying them through an Accumulator.
func SortEvents(events []models.RawEvent) {
	sort.SliceStable(events, func(i, j int) bool { return EventLess(events[i], events[j]) })
}

// SegmentID is cityHash64-equivalent deterministic ID: FNV-1a 64 over
// (video_session_id, start_ms). Stable across rebuilds for the same boundaries.
func SegmentID(sessionID string, start time.Time) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	_, _ = h.Write([]byte{0})
	ms := start.UTC().UnixMilli()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(ms >> (8 * i))
	}
	_, _ = h.Write(buf[:])
	return h.Sum64()
}
