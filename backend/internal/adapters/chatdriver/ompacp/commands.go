package ompacp

import acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are oh-my-pi built-ins kept out of the `/` menu.
//
// OMP advertises only built-ins with a text handler and reserves their names
// before user commands, so a name is enough. These switch the model or effort
// AO's pickers show, rename the session, forget approvals, move the session's
// directory or worktree, open a dashboard on the agent's machine, or pin
// sessions in OMP's own resume list.
var hiddenCommands = acpdriver.HideCommands(
	"model", "models", "switch", "effort", "modelpreset", "fast", "slow",
	"extended-context", "rename", "forget-approvals", "move", "wt", "worktree",
	"trace", "stats", "pin", "unpin",
)
