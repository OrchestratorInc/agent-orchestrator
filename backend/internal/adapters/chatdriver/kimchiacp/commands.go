package kimchiacp

import acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Kimchi built-ins kept out of the `/` menu.
//
// /bug opens a browser on the agent's machine. /language writes a global
// setting and replies through a notification AO does not render, so it looks
// like nothing happened. Skills carry a `skill:` prefix and cannot collide.
var hiddenCommands = acpdriver.HideCommands("bug", "language")
