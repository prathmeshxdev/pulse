package users

import (
	"time"

	"github.com/prathmeshxdev/pulse/internal/deltas"
	"github.com/prathmeshxdev/pulse/internal/models"
)

// EmitAnyOverlap produces ±1 edges for a closed user island.
func EmitAnyOverlap(seg models.UserSegment) []models.UserMinuteDelta {
	if seg.CloseReason == "" {
		return nil
	}
	if !seg.SegmentEnd.After(seg.SegmentStart) {
		return nil
	}
	plus := deltas.StartOfMinute(seg.SegmentStart)
	minus := deltas.StartOfMinute(seg.SegmentEnd.Add(-time.Millisecond)).Add(time.Minute)
	return []models.UserMinuteDelta{
		{Minute: plus, UserSegmentID: seg.UserSegmentID, Delta: 1},
		{Minute: minus, UserSegmentID: seg.UserSegmentID, Delta: -1},
	}
}

// EmitClosedDeltas emits edges only for fully closed user islands.
func EmitClosedDeltas(segs []models.UserSegment) []models.UserMinuteDelta {
	out := make([]models.UserMinuteDelta, 0, len(segs)*2)
	for _, s := range segs {
		out = append(out, EmitAnyOverlap(s)...)
	}
	return out
}

// ConcurrencyCurve builds peak/avg from user deltas (same math as session path).
func ConcurrencyCurve(rows []models.UserMinuteDelta, ids map[uint64]struct{}, rangeStart, rangeEnd time.Time, maxSpanHours int) (peak, avg float64) {
	conv := make([]models.MinuteDelta, len(rows))
	for i, r := range rows {
		conv[i] = models.MinuteDelta{Minute: r.Minute, SegmentID: r.UserSegmentID, Delta: r.Delta}
	}
	_, peak, avg = deltas.ConcurrencyCurve(conv, ids, rangeStart, rangeEnd, maxSpanHours)
	return peak, avg
}
