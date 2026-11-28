package agyacp

import acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Antigravity built-ins kept out of the `/` menu.
//
// agy_acp_server (agy before 1.5.4) advertises plan, logout and usage. `agy acp`
// in later CLIs lists its system commands and skills; its terminal-only commands
// are not known to reach ACP, but they are named here so they cannot appear.
// automation and generative_ui are documented as app-only.
var hiddenCommands = acpdriver.HideCommands(
	"logout", "model", "effort", "rename", "resume", "config", "permissions",
	"rewind", "automation", "generative_ui",
)
