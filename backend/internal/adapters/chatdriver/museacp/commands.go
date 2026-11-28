package museacp

import acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommand keeps Muse commands out of the `/` menu that change its
// settings (model, effort) behind AO's back, or bring in or move sessions and
// configuration from other agents. /compact, the bundled skills and the user's
// own stay.
var hiddenCommand = acpdriver.HideCommands(
	"manage-settings", "import", "migrate", "resume-claude", "resume-codex",
)
