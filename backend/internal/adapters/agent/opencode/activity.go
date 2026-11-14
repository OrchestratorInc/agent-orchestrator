package opencode

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start":
		return domain.ActivityActive, true
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "active":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "permission-blocked":
		return domain.ActivityBlocked, true
	case "permission-resolved":
		return domain.ActivityActive, true
	default:
		return "", false
	}
}
