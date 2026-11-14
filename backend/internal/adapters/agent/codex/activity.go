package codex

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
