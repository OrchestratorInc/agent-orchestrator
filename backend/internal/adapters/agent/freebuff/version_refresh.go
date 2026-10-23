package freebuff

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// Freebuff continues a chat by id, but AO resolves that id from Freebuff's chat
// history rather than from a lifecycle hook, so a release refresh is left to
// the user.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
