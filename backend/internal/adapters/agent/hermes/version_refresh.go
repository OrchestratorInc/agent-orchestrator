package hermes

// SupportsTerminalVersionRefresh leaves unattended release switching gated.
// Hermes admits only the release lines whose terminal chrome AO has qualified
// (qualifiedReleaseLines); an unattended upgrade could leave them. Ordinary
// TUI launch and exact restore stay available.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
