package kimchi

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "post-tool-use-fail":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "notification":
		return notificationState(payload)
	case "session-end":
		return sessionEndState(payload)
	default:
		return "", false
	}
}

func notificationState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		NotificationType string `json:"notification_type"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.NotificationType {
	case "idle_prompt", "agent_completed":
		return domain.ActivityIdle, true
	case "agent_needs_input":
		return domain.ActivityWaitingInput, true
	case "permission_prompt":
		return domain.ActivityBlocked, true
	default:
		return "", false
	}
}

func sessionEndState(payload []byte) (domain.ActivityState, bool) {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Reason {
	case "reload", "new", "resume", "fork":
		return "", false
	default:
		return domain.ActivityExited, true
	}
}
