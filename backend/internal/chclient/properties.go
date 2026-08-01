package chclient

import (
	"encoding/json"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// PropertiesColumn builds a native ClickHouse JSON value for batch insert (v2.47+).
func PropertiesColumn(props map[string]interface{}) *clickhouse.JSON {
	j := clickhouse.NewJSON()
	if len(props) == 0 {
		return j
	}
	for k, v := range props {
		j.SetValueAtPath(k, v)
	}
	return j
}

// PropertiesJSON marshals a properties map to a JSON string (debug/logging).
func PropertiesJSON(props map[string]interface{}) string {
	if len(props) == 0 {
		return "{}"
	}
	b, err := json.Marshal(props)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// PropertiesFromJSON converts a scanned clickhouse.JSON (or legacy string) to a map.
func PropertiesFromJSON(v any) map[string]interface{} {
	switch t := v.(type) {
	case *clickhouse.JSON:
		if t == nil || len(t.ValuesByPath()) == 0 {
			return nil
		}
		return t.NestedMap()
	case clickhouse.JSON:
		if len(t.ValuesByPath()) == 0 {
			return nil
		}
		return t.NestedMap()
	case string:
		return ParsePropertiesJSON(t)
	case []byte:
		return ParsePropertiesJSON(string(t))
	default:
		return nil
	}
}

// ParsePropertiesJSON unmarshals a JSON string cell into a map.
func ParsePropertiesJSON(raw string) map[string]interface{} {
	if raw == "" || raw == "{}" {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
