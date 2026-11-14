package droid

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "notification":
		return domain.ActivityWaitingInput, true
	case "session-end":
		return sessionEndState(payload)
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
	case "clear":
		return "", false
	default:
		return domain.ActivityExited, true
	}
}
