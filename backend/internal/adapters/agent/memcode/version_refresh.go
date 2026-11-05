package memcode

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Memcode has no native restore command in AO yet, so its terminals cannot be
// resumed on a new release without the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
