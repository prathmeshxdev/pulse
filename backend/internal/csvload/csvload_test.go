package csvload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prathmeshxdev/pulse/internal/csvload"
)

func TestReadCSV_ExtraColumnsGoToProperties(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.csv")
	csv := "video_session_id,user_id,content_id,event_type,event,event_timestamp,platform,country,network_type,device_model\n" +
		"s1,u1,100,VideoSessionStart,VideoSessionStart,1700000000000,ANDROID,india,wifi,Pixel8\n"
	require.NoError(t, os.WriteFile(path, []byte(csv), 0o644))

	events, err := csvload.ReadCSV(path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "wifi", events[0].Properties["network_type"])
	assert.Equal(t, "Pixel8", events[0].Properties["device_model"])
	assert.Equal(t, "ANDROID", events[0].Platform)
}

func TestReadCSV_KnownColumnsNotDuplicatedInProperties(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.csv")
	csv := "video_session_id,user_id,content_id,event_type,event,event_timestamp,platform,country\n" +
		"s1,u1,100,VideoSessionStart,VideoSessionStart,1700000000000,ANDROID,india\n"
	require.NoError(t, os.WriteFile(path, []byte(csv), 0o644))

	events, err := csvload.ReadCSV(path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Nil(t, events[0].Properties)
}
