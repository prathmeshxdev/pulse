package deltas

import (
	"time"

	"github.com/prathmeshxdev/pulse/internal/models"
)

// EmitAnyOverlap produces the normative +1/−1 pair for a segment (FINAL_PLAN R6).
//
//	plus_minute  = toStartOfMinute(segment_start)
//	minus_minute = toStartOfMinute(segment_end - 1ms) + 1 minute
//
// Guarantees minus_minute > plus_minute for every non-empty segment.
func EmitAnyOverlap(seg models.Segment) []models.MinuteDelta {
	if !seg.SegmentEnd.After(seg.SegmentStart) {
		return nil
	}
	plus := StartOfMinute(seg.SegmentStart)
	lastActive := seg.SegmentEnd.Add(-time.Millisecond)
	minus := StartOfMinute(lastActive).Add(time.Minute)
	return []models.MinuteDelta{
		{Minute: plus, SegmentID: seg.SegmentID, Delta: 1},
		{Minute: minus, SegmentID: seg.SegmentID, Delta: -1},
	}
}

// EmitAll emits deltas for every segment.
func EmitAll(segs []models.Segment) []models.MinuteDelta {
	out := make([]models.MinuteDelta, 0, len(segs)*2)
	for _, s := range segs {
		out = append(out, EmitAnyOverlap(s)...)
	}
	return out
}

// EmitWide produces the same any-overlap +1/−1 pair but with the segment's
// dimensions denormalized onto each row, for the optional wide rollup.
func EmitWide(seg models.Segment) []models.WideDelta {
	if !seg.SegmentEnd.After(seg.SegmentStart) {
		return nil
	}
	plus := StartOfMinute(seg.SegmentStart)
	minus := StartOfMinute(seg.SegmentEnd.Add(-time.Millisecond)).Add(time.Minute)
	row := func(m time.Time, d int64) models.WideDelta {
		return models.WideDelta{
			Minute: m, Platform: seg.Platform, Country: seg.Country, ContentID: seg.ContentID,
			AppVersion: seg.AppVersion, AudioLanguage: seg.AudioLanguage,
			SubtitleLanguage: seg.SubtitleLanguage, PlayerVersion: seg.PlayerVersion, Delta: d,
		}
	}
	return []models.WideDelta{row(plus, 1), row(minus, -1)}
}

// EmitAllWide emits wide deltas for every segment.
func EmitAllWide(segs []models.Segment) []models.WideDelta {
	out := make([]models.WideDelta, 0, len(segs)*2)
	for _, s := range segs {
		out = append(out, EmitWide(s)...)
	}
	return out
}

// StartOfMinute truncates to the UTC minute boundary.
func StartOfMinute(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
}

// ConcurrencyCurve builds the dense minute curve with opening balance (FINAL_PLAN §2).
// Minutes with no delta carry the previous concurrency forward.
func ConcurrencyCurve(deltas []models.MinuteDelta, segmentIDs map[uint64]struct{}, rangeStart, rangeEnd time.Time, maxSpanHours int) (curve []Point, peak, avg float64) {
	lookback := rangeStart.Add(-time.Duration(maxSpanHours) * time.Hour)

	net := map[time.Time]int64{}
	var opening int64
	for _, d := range deltas {
		if len(segmentIDs) > 0 {
			if _, ok := segmentIDs[d.SegmentID]; !ok {
				continue
			}
		}
		if d.Minute.Before(rangeStart) {
			if !d.Minute.Before(lookback) {
				opening += d.Delta
			}
			continue
		}
		if !d.Minute.Before(rangeEnd) {
			continue
		}
		net[d.Minute] += d.Delta
	}

	if !rangeEnd.After(rangeStart) {
		return nil, 0, 0
	}

	var sum float64
	var maxV float64
	var cur int64 = opening
	n := 0
	for m := rangeStart; m.Before(rangeEnd); m = m.Add(time.Minute) {
		cur += net[m]
		v := float64(cur)
		curve = append(curve, Point{Minute: m, Concurrency: v})
		sum += v
		if n == 0 || v > maxV {
			maxV = v
		}
		n++
	}
	peak = maxV
	if n > 0 {
		avg = sum / float64(n)
	}
	return curve, peak, avg
}

type Point struct {
	Minute      time.Time
	Concurrency float64
}
