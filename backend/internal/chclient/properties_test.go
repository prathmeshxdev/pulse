package chclient_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/prathmeshxdev/pulse/internal/chclient"
)

func TestPropertiesColumn(t *testing.T) {
	j := chclient.PropertiesColumn(nil)
	assert.NotNil(t, j)
	assert.Empty(t, j.ValuesByPath())

	j = chclient.PropertiesColumn(map[string]interface{}{
		"network_type": "wifi",
		"count":        int64(42),
	})
	assert.Equal(t, "wifi", j.ValuesByPath()["network_type"])
	assert.Equal(t, int64(42), j.ValuesByPath()["count"])
}

func TestPropertiesJSON(t *testing.T) {
	assert.Equal(t, "{}", chclient.PropertiesJSON(nil))
	assert.JSONEq(t, `{"network_type":"wifi"}`, chclient.PropertiesJSON(map[string]interface{}{
		"network_type": "wifi",
	}))
}

func TestParsePropertiesJSON(t *testing.T) {
	assert.Nil(t, chclient.ParsePropertiesJSON(""))
	m := chclient.ParsePropertiesJSON(`{"a":"b","n":1}`)
	assert.Equal(t, "b", m["a"])
	assert.Equal(t, float64(1), m["n"])
}

func TestPropertiesFromJSON(t *testing.T) {
	j := chclient.PropertiesColumn(map[string]interface{}{"k": "v"})
	m := chclient.PropertiesFromJSON(j)
	assert.Equal(t, "v", m["k"])
}
