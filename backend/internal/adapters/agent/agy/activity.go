package agy

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "pre-invocation", "post-tool-use":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
