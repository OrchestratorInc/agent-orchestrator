package copilotacp

import acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Copilot CLI built-ins kept out of the `/` menu.
//
// Copilot runs a built-in before a same-named skill, so a name is enough. These
// change the model, the session name, the approval policy or the working
// directory, all of which AO controls. Read from @github/copilot 1.0.62, the
// last release that ships readable JavaScript.
var hiddenCommands = acpdriver.HideCommands(
	"model", "rename", "allow-all", "reset-allowed-tools", "autopilot", "cwd",
)
