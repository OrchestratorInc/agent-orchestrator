package aider

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Aider has file-history restoration but no captured native conversation identity or active-turn lifecycle proof in AO.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
