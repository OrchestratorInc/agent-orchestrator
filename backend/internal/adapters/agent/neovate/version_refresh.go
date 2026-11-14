package neovate

// SupportsTerminalVersionRefresh keeps unattended restarts disabled until
// exact-conversation restore with refreshed instructions is runtime-qualified.
// The Neovate restore audit remains mixed; manual restore is a separate path.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
