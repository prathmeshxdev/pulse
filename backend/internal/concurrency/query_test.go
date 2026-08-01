package concurrency

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/prathmeshxdev/pulse/internal/filters"
)

func TestBuildChartQuery_SummaryWithFilters(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		Grain:  GrainMinute,
		Metric: MetricSummary,
		Filters: []filters.Filter{
			{Dimension: "platform", Op: "eq", Value: "ANDROID"},
			{Dimension: "country", Op: "eq", Value: "india"},
		},
	}, "sony_liv", 72)
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "session_active_segments FINAL")
	assert.Contains(t, q.SQL, "platform = 'ANDROID'")
	assert.Contains(t, q.SQL, "country = 'india'")
	assert.Contains(t, q.SQL, "segment_id IN (SELECT segment_id FROM sel)")
	assert.Contains(t, q.SQL, "opening")
	assert.Contains(t, q.SQL, "numbers(dateDiff('minute'")
	assert.Contains(t, q.SQL, "peak_concurrency")
	assert.Contains(t, q.SQL, "avg_concurrency")
	assert.NotContains(t, q.SQL, "raw_events")
	assert.NotContains(t, strings.ToLower(q.SQL), "inner join sony_liv.session_active_segments")
}

func TestBuildChartQuery_OmitsSelWhenUnfiltered(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Metric: MetricPeak,
	}, "sony_liv", 72)
	require.NoError(t, err)
	assert.NotContains(t, q.SQL, "sel AS")
	assert.NotContains(t, q.SQL, "segment_id IN")
	assert.Contains(t, q.SQL, "peak_concurrency")
}

func TestBuildChartQuery_RejectsBadRange(t *testing.T) {
	_, err := BuildChartQuery(Request{
		Start: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, "sony_liv", 72)
	assert.Error(t, err)
}

func TestBuildChartQuery_UnknownDimension(t *testing.T) {
	_, err := BuildChartQuery(Request{
		Start:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Filters: []filters.Filter{{Dimension: "not_a_dim", Value: "x"}},
	}, "sony_liv", 72)
	assert.Error(t, err)
}

func TestBuildChartQuery_TimeseriesHour(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		Grain:  GrainHour,
		Metric: MetricTimeseries,
	}, "sony_liv", 72)
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "toStartOfHour")
	assert.Contains(t, q.SQL, "GROUP BY")
}
