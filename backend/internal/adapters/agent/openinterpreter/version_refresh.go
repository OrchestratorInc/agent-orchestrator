package openinterpreter

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// AO launches only the inspected release (InspectedRelease); a restart on an
// upgraded binary is refused until its hooks and terminal cues are revalidated.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
