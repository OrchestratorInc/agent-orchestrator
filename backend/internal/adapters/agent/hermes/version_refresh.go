package hermes

// SupportsTerminalVersionRefresh leaves unattended release switching gated.
// Hermes currently admits only the pinned 0.21.6 native distribution; its
// existing explicit restore guard is not qualification for unattended upgrades
// to a different release. Ordinary TUI launch and exact restore stay available.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
