package openjarvis

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// OpenJarvis has no native restore command wired into AO, so its terminals
// cannot be resumed on a new release without the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
