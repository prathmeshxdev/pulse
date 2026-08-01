package segments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/prathmeshxdev/pulse/internal/models"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		eventType, event string
		want             models.Signal
	}{
		{"VideoSessionStart", "", models.SignalOpen},
		{"VideoSessionEnd", "", models.SignalClose},
		{"VideoPlay", "", models.SignalPlay},
		{"AppBackgrounded", "", models.SignalBackground},
		{"AppForegrounded", "", models.SignalForeground},
		{"VideoError", "", models.SignalError},
		{"VideoHeartbeat", "pause", models.SignalPause},
		{"VideoHeartbeat", "speed-pause", models.SignalPause},
		{"VideoHeartbeat", "AdPause", models.SignalPause},
		{"VideoHeartbeat", "resume", models.SignalResume},
		{"VideoHeartbeat", "speed-resume", models.SignalResume},
		{"VideoHeartbeat", "AdResume", models.SignalResume},
		{"VideoHeartbeat", "BufferStart", models.SignalBufferStart},
		{"VideoHeartbeat", "AdBufferStart", models.SignalBufferStart},
		{"VideoHeartbeat", "BufferEnd", models.SignalBufferEnd},
		{"VideoHeartbeat", "AdBufferEnd", models.SignalBufferEnd},
		{"VideoHeartbeat", "buffer-health", models.SignalKeepalive},
		{"VideoHeartbeat", "Seek", models.SignalKeepalive},
		{"VideoHeartbeat", "network-activity", models.SignalKeepalive},
		{"Unknown", "x", models.SignalIgnore},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, Classify(tc.eventType, tc.event), "%s/%s", tc.eventType, tc.event)
	}
}
