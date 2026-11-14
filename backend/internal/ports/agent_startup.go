package ports

import "context"

// AgentStartupGuard holds any shared native launch preferences until the new
// process has loaded and pinned its own selection. AO acquires it before
// preparing launch or restore commands and releases it on every success and
// failure path, after native initialization and initial prompt delivery.
type AgentStartupGuard interface {
	BeginStartup(context.Context, LaunchConfig) (release func(), err error)
}

// AgentStartupTerminal is the current terminal surface of one newly started
// agent process. Output must preserve rendered cell styles and exclude raw
// scrollback. SendInput sends raw terminal bytes without appending a newline or
// applying the normal user-message paste protocol.
type AgentStartupTerminal interface {
	Output(context.Context) (string, error)
	SendInput(context.Context, string) error
}

// AgentStartupInitializer selects process-local native state before AO exposes
// a successful launch or delivers its initial task. It runs for fresh launches
// and native restores, including launches with no task. Implementations must
// confirm native state and fail closed on unknown terminal surfaces.
type AgentStartupInitializer interface {
	InitializeTerminal(context.Context, LaunchConfig, AgentStartupTerminal) error
}
