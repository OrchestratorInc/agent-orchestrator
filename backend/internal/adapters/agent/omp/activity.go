package omp

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "stop":
		return domain.ActivityIdle, true
	case "user-prompt-submit", "permission-resolved":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	case "session-end":
		return domain.ActivityExited, true
	default:
		return "", false
	}
}
