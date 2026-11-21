package stakpak

// SupportsTerminalVersionRefresh leaves unattended release switching off.
// Stakpak reports no session id through a hook: AO infers it from Stakpak's
// session list, which is good enough for a restore the user asks for but not
// for restarting an idle session nobody is watching.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
