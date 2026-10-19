package unrealagent

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Unreal Agent is Chat-only and has no interactive terminal interface.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
