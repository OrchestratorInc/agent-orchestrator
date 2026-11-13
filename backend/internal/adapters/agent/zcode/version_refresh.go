package zcode

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// ZCode's terminal restore is the cwd-scoped `zcode -c` (latest session for the
// worktree), not a captured native conversation id, and the CLI ships inside
// the desktop app with no installer AO could run, so its terminals cannot be
// resumed on a new release without the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
