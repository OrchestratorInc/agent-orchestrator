package pool

// SupportsTerminalVersionRefresh refuses terminal refresh for this Chat-only integration.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
