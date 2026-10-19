package zcode

import "github.com/aoagents/agent-orchestrator/backend/internal/ports"

// TerminalMessaging declares that a zcode terminal session runs one-shot. The
// launch is `zcode -p <prompt>` (see GetLaunchCommand): the CLI streams its
// reply and exits, leaving only AO's keep-alive shell in the pane. A follow-up
// pasted there would go to the exiting CLI's stdin or be executed by that
// shell, so the Session Manager refuses `ao send` to a zcode TUI session
// (HARNESS_ONE_SHOT) instead of typing it. The declaration is per harness: a
// restored generation (GetRestoreCommand's `zcode -c`, an interactive TUI) is
// refused the same way because nothing on the session record distinguishes
// it from the launch. A chat-driver session (the mapped app-server follow-up)
// is relayed through its conversation and never consults it.
func (p *Plugin) TerminalMessaging() ports.AgentTerminalMessaging {
	return ports.AgentTerminalMessagingOneShot
}

var _ ports.AgentTerminalMessagingDeclarer = (*Plugin)(nil)
