package grokacp

import acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Grok CLI built-ins kept out of the `/` menu.
//
// always-approve changes the approval policy that AO's permission picker owns.
// Grok qualifies a skill whose name a built-in takes, so a name is enough.
var hiddenCommands = acpdriver.HideCommands("always-approve")
