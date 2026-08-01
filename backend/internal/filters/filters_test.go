package filters_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prathmeshxdev/pulse/internal/filters"
)

func TestBuildSegmentPredicates_RootAndDict(t *testing.T) {
	preds, has, err := filters.BuildSegmentPredicates([]filters.Filter{
		{Dimension: "platform", Op: "eq", Value: "ANDROID"},
		{Dimension: "video_type", Op: "eq", Value: "live"},
	}, "sony_liv", nil)
	require.NoError(t, err)
	require.True(t, has)
	assert.Contains(t, preds[0], "platform = 'ANDROID'")
	assert.Contains(t, preds[1], "dictGet('sony_liv.content_dict', 'video_type', content_id) = 'live'")
}

func TestBuildSegmentPredicates_TypedPropertyString(t *testing.T) {
	preds, _, err := filters.BuildSegmentPredicates([]filters.Filter{
		{Dimension: "network_type", Op: "eq", Value: "wifi"},
	}, "sony_liv", filters.StringFallbackTypes{})
	require.NoError(t, err)
	assert.Contains(t, preds[0], "toString(properties.network_type) = 'wifi'")
}

func TestBuildSegmentPredicates_TypedPropertyNumeric(t *testing.T) {
	types := filters.PropertyTypes{"bandwidth_mbps": "Int64"}
	preds, _, err := filters.BuildSegmentPredicates([]filters.Filter{
		{Dimension: "bandwidth_mbps", Op: "eq", Value: "100"},
	}, "sony_liv", filters.StringFallbackTypes{PropertyTypes: types})
	require.NoError(t, err)
	assert.Contains(t, preds[0], "properties.bandwidth_mbps = 100")
	assert.NotContains(t, preds[0], "toString")
}

func TestValueSuggestionExpr_Property(t *testing.T) {
	r, ok := filters.ResolveDimension("network_type", "sony_liv", filters.StringFallbackTypes{})
	require.True(t, ok)
	expr := filters.ValueSuggestionExpr(r, "sony_liv", filters.StringFallbackTypes{})
	assert.Equal(t, "toString(properties.network_type)", expr)
}

func TestPropertyDimensions(t *testing.T) {
	dims := filters.PropertyDimensions(filters.PropertyTypes{
		"network_type": "String",
		"score":        "Int64",
	})
	require.Len(t, dims, 2)
}
