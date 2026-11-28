package tau

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Tau restore depends on native history being on disk and is refused without
// it, a path not yet audited for resuming terminals on a new release, so Tau
// terminals are not resumed on a new release without the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
