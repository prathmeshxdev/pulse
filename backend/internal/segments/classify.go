package segments

import "github.com/prathmeshxdev/pulse/internal/models"

// pauseEvents and resumeEvents ride inside event_type='VideoHeartbeat' (FINAL_PLAN §1.3).
var pauseEvents = map[string]struct{}{
	"pause":       {},
	"speed-pause": {},
	"AdPause":     {},
}

var resumeEvents = map[string]struct{}{
	"resume":       {},
	"speed-resume": {},
	"AdResume":     {},
}

// Classify maps (event_type, event) → signal. This is the ONLY place event semantics
// are defined — keep in lockstep with FINAL_PLAN §1.3 / SEMANTICS_SPEC §2.
func Classify(eventType, event string) models.Signal {
	switch eventType {
	case "VideoSessionStart":
		return models.SignalOpen
	case "VideoSessionEnd":
		return models.SignalClose
	case "VideoPlay":
		return models.SignalPlay
	case "AppBackgrounded":
		return models.SignalBackground
	case "AppForegrounded":
		return models.SignalForeground
	case "VideoError":
		return models.SignalError
	case "VideoHeartbeat":
		if _, ok := pauseEvents[event]; ok {
			return models.SignalPause
		}
		if _, ok := resumeEvents[event]; ok {
			return models.SignalResume
		}
		// BufferStart / BufferEnd / Seek / quality / network → keepalive (R2).
		return models.SignalKeepalive
	default:
		return models.SignalIgnore
	}
}
