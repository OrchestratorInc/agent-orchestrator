package cursoracp

import acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Cursor CLI built-ins kept out of the `/` menu.
//
// Cursor's only advertised built-in copies a request id to the clipboard of the
// machine the agent runs on. Everything else in its list is a user, team or
// project command, or a skill.
var hiddenCommands = acpdriver.HideCommands("copy-request-id")
