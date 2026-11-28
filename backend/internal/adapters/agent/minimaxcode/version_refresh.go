package minimaxcode

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// MiniMax restore validates native history whose format is qualified only on
// 0.6.5, so a running terminal is not resumed on a new release without the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
