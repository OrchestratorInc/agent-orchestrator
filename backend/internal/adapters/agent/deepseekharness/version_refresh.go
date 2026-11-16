package deepseekharness

// SupportsTerminalVersionRefresh declares unattended exact-conversation resume.
// DeepSeek terminal sessions expose no native-ID lifecycle hook; its headless restore also requires a new task.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }
