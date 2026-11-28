package vibe

import (
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// DeriveActivityState maps Vibe's available hook events conservatively. A
// pre_tool callback happens before any permission prompt, so it is evidence of
// work but not evidence that the agent is waiting for input.
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
