package codex

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState maps a Codex hook event onto an AO activity state. The
// bool is false when the event carries no activity signal.
//
// event is the AO hook sub-command name installed in codexManagedHooks
// ("user-prompt-submit", "permission-request", "stop", ...), not the native
// Codex event name. Codex currently has no SessionEnd/Notification equivalent
// in the adapter, so runtime exit still falls back to the reaper.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		// waiting_input, not blocked: Codex's only PostToolUse hook tracks
		// successful subagent spawns, not approval resolution. A blocked
		// state could therefore remain stuck until the turn ends.
		return domain.ActivityWaitingInput, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
