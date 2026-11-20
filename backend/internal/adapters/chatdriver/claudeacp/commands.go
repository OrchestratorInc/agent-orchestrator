package claudeacp

import acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"

// hiddenCommands are Claude Code built-ins kept out of the `/` menu.
//
// claude-agent-acp forwards the CLI's whole command list and strips only a few
// names, and Claude reports skills and built-ins together, so AO names the
// built-ins. Each of these changes a choice AO's own controls own, and AO's
// controls would not show the change: /model, /effort and /fast belong to the
// model menu (AO's /model opens it), and /auto-mode-setup to the approval mode.
// Sent as chat text, /model also only printed Claude's terminal usage.
var hiddenCommands = acpdriver.HideCommands("model", "effort", "fast", "auto-mode-setup")
