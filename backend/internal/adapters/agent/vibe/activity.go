package vibe

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "pre-tool", "post-tool":
		return domain.ActivityActive, true
	case "post-agent":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
