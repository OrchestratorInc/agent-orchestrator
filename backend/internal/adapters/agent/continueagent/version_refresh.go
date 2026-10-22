package continueagent

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Continue --resume selects the latest session and --fork <id> creates a new native conversation.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
