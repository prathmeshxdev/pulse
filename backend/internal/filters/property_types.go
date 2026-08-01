package filters

import "strings"

// PropertyTypes maps a dynamic properties key to its ClickHouse type name
// (from properties_key_mappings daily append MV).
type PropertyTypes map[string]string

// PropertyTypeResolver supplies ClickHouse type names for dynamic property keys.
type PropertyTypeResolver interface {
	PropertyCHType(key string) (string, bool)
}

func (p PropertyTypes) PropertyCHType(key string) (string, bool) {
	if p == nil {
		return "", false
	}
	t, ok := p[key]
	return t, ok
}

// StringFallbackTypes resolves unknown property keys as String (safe default when
// the refreshable MV has not seen a key yet).
type StringFallbackTypes struct {
	PropertyTypes
}

func (s StringFallbackTypes) PropertyCHType(key string) (string, bool) {
	if t, ok := s.PropertyTypes.PropertyCHType(key); ok {
		return t, true
	}
	return "String", true
}

// ResolvedDimension is the storage location and type for a filterable dimension.
type ResolvedDimension struct {
	Kind   string // segment | dict | property
	Ref    string
	CHType string
}

// ResolveDimension maps a filter dimension name to its storage.
func ResolveDimension(dim, database string, propTypes PropertyTypeResolver) (ResolvedDimension, bool) {
	kind, ref, ok := Lookup(dim)
	if !ok {
		return ResolvedDimension{}, false
	}
	r := ResolvedDimension{Kind: kind, Ref: ref}
	switch kind {
	case "segment":
		if ref == "content_id" {
			r.CHType = "UInt64"
		} else {
			r.CHType = "String"
		}
	case "dict":
		r.CHType = "String"
	case "property":
		if propTypes != nil {
			if t, ok := propTypes.PropertyCHType(ref); ok {
				r.CHType = t
			} else {
				r.CHType = "String"
			}
		} else {
			r.CHType = "String"
		}
	}
	_ = database
	return r, true
}

// FilterExpr returns the SQL expression for filtering/breakdown on a dimension.
// Property keys use typed access (numeric paths compare natively; others toString).
func FilterExpr(r ResolvedDimension, database string, propTypes PropertyTypeResolver) string {
	switch r.Kind {
	case "segment":
		return r.Ref
	case "dict":
		return "dictGet('" + database + ".content_dict', '" + r.Ref + "', content_id)"
	case "property":
		return typedPropertyAccess(r.Ref, propTypes, false)
	default:
		return r.Ref
	}
}

// ValueSuggestionExpr returns a stringifiable SQL expression for DISTINCT value
// pickers (dropdowns). All kinds normalize to String for the UI.
func ValueSuggestionExpr(r ResolvedDimension, database string, propTypes PropertyTypeResolver) string {
	switch r.Kind {
	case "segment":
		if r.Ref == "content_id" {
			return "toString(" + r.Ref + ")"
		}
		return r.Ref
	case "dict":
		return r.Ref
	case "property":
		return typedPropertyAccess(r.Ref, propTypes, true)
	default:
		return r.Ref
	}
}

// NonEmptyPredicate is the WHERE clause guard for value pickers / breakdown GROUP BY.
func NonEmptyPredicate(r ResolvedDimension, expr string) string {
	switch r.Kind {
	case "segment":
		if r.Ref == "content_id" {
			return r.Ref + " > 0"
		}
		return expr + " != ''"
	case "dict":
		return expr + " != ''"
	case "property":
		if isNumericCHType(r.CHType) {
			return "properties." + r.Ref + " IS NOT NULL"
		}
		return expr + " != ''"
	default:
		return expr + " != ''"
	}
}

func typedPropertyAccess(key string, propTypes PropertyTypeResolver, forceString bool) string {
	base := "properties." + key
	if forceString {
		return "toString(" + base + ")"
	}
	chType := "String"
	if propTypes != nil {
		if t, ok := propTypes.PropertyCHType(key); ok {
			chType = t
		}
	}
	if isNumericCHType(chType) || chType == "Bool" {
		return base
	}
	return "toString(" + base + ")"
}

func isNumericCHType(t string) bool {
	return strings.HasPrefix(t, "Int") ||
		strings.HasPrefix(t, "UInt") ||
		strings.HasPrefix(t, "Float") ||
		t == "Double" ||
		strings.HasPrefix(t, "Decimal")
}

// BreakdownValueExpr returns the GROUP BY expression for top-N breakdown on segments.
func BreakdownValueExpr(r ResolvedDimension, database string, propTypes PropertyTypeResolver) string {
	if r.Kind == "dict" {
		return FilterExpr(r, database, propTypes)
	}
	return ValueSuggestionExpr(r, database, propTypes)
}

func isNumericDimension(r ResolvedDimension) bool {
	if r.Kind == "segment" && r.Ref == "content_id" {
		return true
	}
	if r.Kind == "property" && isNumericCHType(r.CHType) {
		return true
	}
	return false
}
