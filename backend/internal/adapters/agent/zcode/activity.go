package zcode

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState uses native lifecycle boundaries. PermissionRequest runs
// when the provider is about to ask; it must never be treated as idle input.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
