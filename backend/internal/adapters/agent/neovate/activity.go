package neovate

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// DeriveActivityState interprets only native events actually exposed by Neovate.
// Tool start is not evidence of a permission dialog. Coverage remains partial.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit", "active":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
