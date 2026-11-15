package activitystate

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func StandardDeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start":
		return domain.ActivityActive, true
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "pre-tool-use", "post-tool-use", "post-tool-use-failure", "permission-resolved":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "permission-request":
		return domain.ActivityWaitingInput, true
	default:
		return "", false
	}
}
