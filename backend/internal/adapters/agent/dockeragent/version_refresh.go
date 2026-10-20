package dockeragent

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Docker Agent runs as native ACP Chat with no terminal UI to resume, so it
// does not participate in terminal version refresh.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
