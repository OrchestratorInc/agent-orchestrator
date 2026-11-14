package aider

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	if event == "notification" {
		return domain.ActivityWaitingInput, true
	}
	return "", false
}
