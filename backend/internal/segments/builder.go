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

// BuildSession implements FINAL_PLAN §1.4–§1.5 state machine for one session.
func (b *Builder) BuildSession(sessionID string, events []models.RawEvent, watermark time.Time) []models.Segment {
	if len(events) == 0 {
		return nil
	}
	sort.SliceStable(events, func(i, j int) bool {
		return eventLess(events[i], events[j])
	})

	st := sessionState{
		// R5 / §1.4: session_open defaults true from first event; open sets it explicitly.
		sessionOpen:  true,
		foreground:   true,  // default true
		playing:      false, // default false
		grace:        b.cfg.HeartbeatGrace(),
		pauseActive:  !b.cfg.PauseCountsAsActive, // when false, pause closes segment
		bufferActive: b.cfg.BufferingCountsActive,
	}

	var segs []models.Segment
	for _, e := range events {
		signal := Classify(e.EventType, e.Event)
		if st.closed {
			// R5: close is terminal — ignore later events.
			continue
		}

		// Heartbeat gap before applying this event (R4).
		if st.inActive && !st.lastKeepalive.IsZero() {
			gapEnd := st.lastKeepalive.Add(st.grace)
			if e.EventTimestamp.After(gapEnd) {
				segs = append(segs, st.closeSegment(gapEnd, models.CloseReasonHeartbeat, false, b.version))
			}
		}

		switch signal {
		case models.SignalOpen:
			st.sessionOpen = true
			st.foreground = true
			// Only reset playing when not already in an active segment. Same-ms
			// VideoPlay can sort before VideoSessionStart by (event_type, event);
			// clearing playing mid-segment would stall keepalive refresh.
			if !st.inActive {
				st.playing = false
			}
			// Wait for play/keepalive evidence before opening a segment.

		case models.SignalPlay:
			st.playing = true
			st.lastKeepalive = e.EventTimestamp
			st.maybeOpen(e)

		case models.SignalKeepalive:
			// Gate is mandatory: foreground AND playing (SEMANTICS_SPEC §2).
			if st.foreground && st.playing && st.sessionOpen {
				if !st.inActive {
					st.openSegment(e)
				} else {
					st.lastKeepalive = e.EventTimestamp
				}
			}

		case models.SignalPause:
			if st.pauseActive {
				// Locked D2/R1: pause ends the active segment.
				if st.inActive {
					segs = append(segs, st.closeSegment(e.EventTimestamp, models.CloseReasonPause, false, b.version))
				}
				st.playing = false
			} else {
				// Sensitivity flip: pause counts as active → treat as keepalive.
				st.playing = true
				st.lastKeepalive = e.EventTimestamp
				st.maybeOpen(e)
			}

		case models.SignalResume:
			// No-op if already playing (31,780 resume vs 27,340 pause asymmetry).
			if !st.playing {
				st.playing = true
				st.lastKeepalive = e.EventTimestamp
				st.maybeOpen(e)
			}

		case models.SignalBackground:
			if st.inActive {
				segs = append(segs, st.closeSegment(e.EventTimestamp, models.CloseReasonBackground, false, b.version))
			}
			st.foreground = false

		case models.SignalForeground:
			st.foreground = true
			// R3: foreground alone does not restart; wait for play/resume/keepalive.

		case models.SignalError:
			// R5: error closes the segment only, not the session.
			if st.inActive {
				segs = append(segs, st.closeSegment(e.EventTimestamp, models.CloseReasonError, false, b.version))
			}
			st.playing = false

		case models.SignalClose:
			if st.inActive {
				segs = append(segs, st.closeSegment(e.EventTimestamp, models.CloseReasonSessionEnd, true, b.version))
			}
			st.sessionOpen = false
			st.closed = true
			st.playing = false

		case models.SignalIgnore:
			// no-op
		}
		_ = st.bufferActive // reserved for sensitivity flip of D3
	}

	// Still active at end of known data → clamp to watermark (R8).
	if st.inActive && !st.closed {
		end := st.lastKeepalive.Add(st.grace)
		if !watermark.IsZero() && end.After(watermark) {
			end = watermark
		}
		segs = append(segs, st.closeSegment(end, models.CloseReasonWatermark, false, b.version))
	}

	// R7: drop zero-length segments.
	filtered := segs[:0]
	for _, s := range segs {
		if s.SegmentEnd.After(s.SegmentStart) {
			s.VideoSessionID = sessionID
			filtered = append(filtered, s)
		}
	}
	return filtered
}

type sessionState struct {
	sessionOpen  bool
	closed       bool
	foreground   bool
	playing      bool
	inActive     bool
	segmentStart time.Time
	lastKeepalive time.Time
	dims         models.RawEvent
	grace        time.Duration
	pauseActive  bool // true means pause closes (normal locked semantics)
	bufferActive bool
}

func (st *sessionState) maybeOpen(e models.RawEvent) {
	if st.sessionOpen && st.foreground && st.playing && !st.inActive {
		st.openSegment(e)
	}
}

func (st *sessionState) openSegment(e models.RawEvent) {
	st.inActive = true
	st.segmentStart = e.EventTimestamp
	st.lastKeepalive = e.EventTimestamp
	// R10: snapshot dimensions deterministically at segment start.
	st.dims = e
}

func (st *sessionState) closeSegment(end time.Time, reason string, isFinal bool, version uint64) models.Segment {
	st.inActive = false
	final := uint8(0)
	if isFinal {
		final = 1
	}
	seg := models.Segment{
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
	}
	return seg
}

func eventLess(a, b models.RawEvent) bool {
	if !a.EventTimestamp.Equal(b.EventTimestamp) {
		return a.EventTimestamp.Before(b.EventTimestamp)
	}
	if a.EventType != b.EventType {
		return a.EventType < b.EventType
	}
	return a.Event < b.Event
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
