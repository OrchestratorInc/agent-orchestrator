package amp

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "stop":
		var native struct {
			Status string `json:"status"`
		}
		if len(payload) > 0 && json.Unmarshal(payload, &native) != nil {
			return "", false
		}
		if native.Status == "error" {
			return domain.ActivityWaitingInput, true
		}
		return domain.ActivityIdle, true
	case "thread-state":
		var native struct {
			State string `json:"state"`
		}
		if json.Unmarshal(payload, &native) != nil {
			return "", false
		}
		switch native.State {
		case "running":
			return domain.ActivityActive, true
		case "idle":
			return domain.ActivityIdle, true
		case "awaiting-approval", "error":
			return domain.ActivityWaitingInput, true
		default:
			return "", false
		}
	default:
		return "", false
	}
}
