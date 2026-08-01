package deltas

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/prathmeshxdev/pulse/internal/models"
)

func TestEmitAnyOverlap_SubMinute(t *testing.T) {
	seg := models.Segment{
		SegmentID:    1,
		SegmentStart: time.Date(2026, 1, 15, 10, 0, 10, 0, time.UTC),
		SegmentEnd:   time.Date(2026, 1, 15, 10, 0, 50, 0, time.UTC),
	}
	d := EmitAnyOverlap(seg)
	require.Len(t, d, 2)
	assert.Equal(t, int64(1), d[0].Delta)
	assert.Equal(t, int64(-1), d[1].Delta)
	assert.Equal(t, time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC), d[0].Minute)
	assert.Equal(t, time.Date(2026, 1, 15, 10, 1, 0, 0, time.UTC), d[1].Minute)
	assert.True(t, d[1].Minute.After(d[0].Minute))
}

func TestEmitAnyOverlap_ZeroLengthDropped(t *testing.T) {
	seg := models.Segment{
		SegmentID:    1,
		SegmentStart: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		SegmentEnd:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
	}
	assert.Nil(t, EmitAnyOverlap(seg))
}

func TestConcurrencyCurve_OpeningBalance(t *testing.T) {
	// +1 at 09:00, -1 at 10:30
	d := []models.MinuteDelta{
		{Minute: time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC), SegmentID: 1, Delta: 1},
		{Minute: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC), SegmentID: 1, Delta: -1},
	}
	start := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)
	curve, peak, avg := ConcurrencyCurve(d, nil, start, end, 72)
	require.Len(t, curve, 60)
	for _, p := range curve {
		assert.GreaterOrEqual(t, p.Concurrency, 0.0)
	}
	assert.Equal(t, 1.0, peak)
	assert.InDelta(t, 0.5, avg, 0.001)
}
