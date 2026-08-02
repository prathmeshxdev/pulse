package filters

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/prathmeshxdev/pulse/internal/querybuilder"
)

var propertyNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Allowed dimension columns on session_active_segments (typed only — D1).
var segmentDimensions = map[string]string{
	"platform":          "platform",
	"country":           "country",
	"content_id":        "content_id",
	"app_version":       "app_version",
	"audio_language":    "audio_language",
	"subtitle_language": "subtitle_language",
	"player_version":    "player_version",
	"user_id":           "user_id",
}

// Content-dictionary attributes: 1:1 with content_id, resolved via dictGet
// (SCHEMA_AND_DDL decision tree) rather than denormalised onto segments.
var dictDimensions = map[string]string{
	"title":      "title",
	"video_type": "video_type",
	"category":   "category",
	"show_name":  "show_name",
}

// rollupDimensions are the event dims denormalized onto concurrency_minute_serving.
// Content-dict dims (video_type/category/title) and user_id are NOT in the rollup.
var rollupDimensions = map[string]string{
	"platform":          "platform",
	"country":           "country",
	"content_id":        "content_id",
	"app_version":       "app_version",
	"audio_language":    "audio_language",
	"subtitle_language": "subtitle_language",
	"player_version":    "player_version",
}

// RollupSupported reports whether every filter targets a rollup column (so the
// wide rollup can serve the query without a segment semi-join).
func RollupSupported(fs []Filter) bool {
	for _, f := range fs {
		if _, ok := rollupDimensions[strings.ToLower(strings.TrimSpace(f.Dimension))]; !ok {
			return false
		}
	}
	return true
}

// BuildRollupPredicates builds direct column predicates for the wide rollup.
func BuildRollupPredicates(fs []Filter) ([]string, error) {
	out := make([]string, 0, len(fs))
	for i, f := range fs {
		dim := strings.ToLower(strings.TrimSpace(f.Dimension))
		col, ok := rollupDimensions[dim]
		if !ok {
			return nil, fmt.Errorf("filter[%d]: %q is not a rollup dimension", i, f.Dimension)
		}
		op := strings.ToLower(strings.TrimSpace(f.Op))
		if op == "" {
			op = "eq"
		}
		pred, err := buildPred(col, op, f, dim == "content_id")
		if err != nil {
			return nil, fmt.Errorf("filter[%d]: %w", i, err)
		}
		out = append(out, pred)
	}
	return out, nil
}

// Filter is a single equality (or IN) predicate on a known dimension.
type Filter struct {
	Dimension string   `json:"dimension"`
	Op        string   `json:"op"` // eq | in
	Value     string   `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
}

// BuildSegmentPredicates returns SQL predicates for the sel CTE.
// Returns (predicates, hasDimensionFilter). propTypes supplies ClickHouse types
// for dynamic properties keys (from properties_key_mappings MV); nil → string fallback.
func BuildSegmentPredicates(fs []Filter, database string, propTypes PropertyTypeResolver) ([]string, bool, error) {
	if len(fs) == 0 {
		return nil, false, nil
	}
	if propTypes == nil {
		propTypes = StringFallbackTypes{}
	}
	out := make([]string, 0, len(fs))
	for i, f := range fs {
		dim := strings.ToLower(strings.TrimSpace(f.Dimension))
		op := strings.ToLower(strings.TrimSpace(f.Op))
		if op == "" {
			op = "eq"
		}

		resolved, ok := ResolveDimension(dim, database, propTypes)
		if !ok {
			return nil, false, fmt.Errorf("unknown dimension %q", f.Dimension)
		}
		expr := FilterExpr(resolved, database, propTypes)
		pred, err := buildPred(expr, op, f, isNumericDimension(resolved))
		if err != nil {
			return nil, false, fmt.Errorf("filter[%d]: %w", i, err)
		}
		out = append(out, pred)
	}
	return out, true, nil
}

func buildPred(left, op string, f Filter, numeric bool) (string, error) {
	lit := func(v string) (string, error) {
		if numeric {
			if v == "" {
				return "", fmt.Errorf("empty numeric value")
			}
			for _, ch := range v {
				if ch < '0' || ch > '9' {
					return "", fmt.Errorf("numeric dimension requires digits, got %q", v)
				}
			}
			return v, nil
		}
		return querybuilder.QuoteString(v), nil
	}
	switch op {
	case "eq", "=":
		if f.Value == "" && len(f.Values) == 1 {
			f.Value = f.Values[0]
		}
		if f.Value == "" {
			return "", fmt.Errorf("eq filter requires value")
		}
		right, err := lit(f.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s = %s", left, right), nil
	case "in":
		vals := f.Values
		if len(vals) == 0 && f.Value != "" {
			vals = []string{f.Value}
		}
		if len(vals) == 0 {
			return "", fmt.Errorf("in filter requires values")
		}
		quoted := make([]string, len(vals))
		for i, v := range vals {
			r, err := lit(v)
			if err != nil {
				return "", err
			}
			quoted[i] = r
		}
		return fmt.Sprintf("%s IN (%s)", left, strings.Join(quoted, ", ")), nil
	default:
		return "", fmt.Errorf("unsupported op %q", op)
	}
}

// StaticDimensions lists the fixed typed segment + dict dimensions.
func StaticDimensions() []DimensionMeta {
	return dimensionsList()
}

// Dimensions lists filterable dimensions for GET /schema/dimensions (static only).
// Callers merge PropertyDimensions for dynamic keys from properties_key_mappings.
func Dimensions() []DimensionMeta {
	return dimensionsList()
}

func dimensionsList() []DimensionMeta {
	out := make([]DimensionMeta, 0, len(segmentDimensions)+len(dictDimensions))
	for k := range segmentDimensions {
		out = append(out, DimensionMeta{Name: k, Source: "session_active_segments", Type: "LowCardinality(String)"})
	}
	for k := range dictDimensions {
		out = append(out, DimensionMeta{Name: k, Source: "content_dict", Type: "String"})
	}
	return out
}

// PropertyDimensions builds dimension metadata from the properties_key_mappings catalog.
func PropertyDimensions(types PropertyTypes) []DimensionMeta {
	if len(types) == 0 {
		return nil
	}
	out := make([]DimensionMeta, 0, len(types))
	for k, t := range types {
		out = append(out, DimensionMeta{Name: k, Source: "properties", Type: t})
	}
	return out
}

type DimensionMeta struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Type   string `json:"type"`
}

// Lookup resolves a dimension to its storage. kind is "segment" (typed column
// on session_active_segments), "dict" (content_dict attribute), or "property"
// (dynamic JSON path under session_active_segments.properties).
// ref is the column/attribute/property name. ok is false for invalid names.
func Lookup(dim string) (kind, ref string, ok bool) {
	dim = strings.ToLower(strings.TrimSpace(dim))
	if col, found := segmentDimensions[dim]; found {
		return "segment", col, true
	}
	if attr, found := dictDimensions[dim]; found {
		return "dict", attr, true
	}
	if propertyNameRE.MatchString(dim) {
		return "property", dim, true
	}
	return "", "", false
}
