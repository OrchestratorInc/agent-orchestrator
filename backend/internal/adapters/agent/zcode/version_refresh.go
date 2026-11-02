package zcode

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// ZCode can resume a session by id (`--resume <sessionId>`), but this adapter has
// no trusted source for that id yet: project hooks need a human-run trust grant,
// so AO cannot prove which native conversation a terminal launch belongs to.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
