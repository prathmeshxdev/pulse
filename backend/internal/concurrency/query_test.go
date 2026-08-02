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
	}, "sony_liv", 72, nil)
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
	}, "sony_liv", 72, nil)
	require.NoError(t, err)
	assert.NotContains(t, q.SQL, "sel AS")
	assert.NotContains(t, q.SQL, "segment_id IN")
	assert.Contains(t, q.SQL, "peak_concurrency")
}

func TestBuildChartQuery_RejectsBadRange(t *testing.T) {
	_, err := BuildChartQuery(Request{
		Start: time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
	}, "sony_liv", 72, nil)
	assert.Error(t, err)
}

func TestBuildChartQuery_DynamicPropertyDimension(t *testing.T) {
	types := filters.PropertyTypes{"network_type": "String"}
	q, err := BuildChartQuery(Request{
		Start:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Filters: []filters.Filter{{Dimension: "network_type", Op: "eq", Value: "wifi"}},
	}, "sony_liv", 72, filters.StringFallbackTypes{PropertyTypes: types})
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "toString(properties.network_type) = 'wifi'")
}

func TestBuildChartQuery_DynamicPropertyNumeric(t *testing.T) {
	types := filters.PropertyTypes{"bandwidth_mbps": "Int64"}
	q, err := BuildChartQuery(Request{
		Start:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Filters: []filters.Filter{{Dimension: "bandwidth_mbps", Op: "eq", Value: "100"}},
	}, "sony_liv", 72, filters.StringFallbackTypes{PropertyTypes: types})
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "properties.bandwidth_mbps = 100")
}

func TestBuildChartQuery_UnknownDimension(t *testing.T) {
	_, err := BuildChartQuery(Request{
		Start:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:     time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC),
		Filters: []filters.Filter{{Dimension: "not-a-dim", Value: "x"}},
	}, "sony_liv", 72, nil)
	assert.Error(t, err)
}

// TestBuildChartQuery_OpenEdgesCorrection locks in the two properties that
// silently regress the live-open correction if lost: filtering on
// close_reason (not is_final, which means something else entirely — see the
// doc comment in query.go) and mirroring deltas.EmitAnyOverlap's exact
// minus-edge rounding. Both were the root cause of real overcounts during
// benchmarking (see cmd/bench_livecorrection history) before landing here.
func TestBuildChartQuery_OpenEdgesCorrection(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		Grain:  GrainMinute,
		Metric: MetricSummary,
		Filters: []filters.Filter{
			{Dimension: "platform", Op: "eq", Value: "ANDROID"},
		},
	}, "sony_liv", 72, nil)
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "open_edges")
	assert.Contains(t, q.SQL, "close_reason = ''")
	assert.NotContains(t, q.SQL, "is_final = 0")
	assert.NotContains(t, q.SQL, "is_final=0")
	assert.Contains(t, q.SQL, "subtractMilliseconds(segment_end, 1)")
	// Dimension filters apply directly to open_edges (typed columns), not
	// just via the segment_id semi-join used for the historical side.
	assert.Contains(t, q.SQL, "platform = 'ANDROID'")
	countPlatformPreds := strings.Count(q.SQL, "platform = 'ANDROID'")
	assert.GreaterOrEqual(t, countPlatformPreds, 2, "platform predicate should appear in both sel and open_edges")
}

func TestBuildChartQuery_UserUnit(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		Metric: MetricPeak,
		Unit:   UnitUser,
		Filters: []filters.Filter{
			{Dimension: "platform", Op: "eq", Value: "ANDROID"},
		},
	}, "sony_liv", 72, nil)
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "user_active_segments")
	assert.Contains(t, q.SQL, "user_minute_deltas")
	assert.Contains(t, q.SQL, "user_segment_id IN (SELECT user_segment_id FROM sel)")
	assert.Contains(t, q.SQL, "GROUP BY user_id")
	assert.NotContains(t, q.SQL, "sony_liv.minute_deltas")
}

func TestBuildChartQuery_TimeseriesHour(t *testing.T) {
	q, err := BuildChartQuery(Request{
		Start:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		Grain:  GrainHour,
		Metric: MetricTimeseries,
	}, "sony_liv", 72, nil)
	require.NoError(t, err)
	assert.Contains(t, q.SQL, "toStartOfHour")
	assert.Contains(t, q.SQL, "GROUP BY")
}
