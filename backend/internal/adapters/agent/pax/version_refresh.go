package pax

// SupportsTerminalVersionRefresh leaves unattended release switching off. AO
// cannot tell from the screen alone that an idle Pax holds no unsaved draft
// or open question, and Pax saves a conversation only after its first turn.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
