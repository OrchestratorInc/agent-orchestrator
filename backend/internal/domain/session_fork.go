package domain

import "time"

// SessionForkContextMode says how a fork's agent got its memory of the
// conversation.
type SessionForkContextMode string

// Fork context modes.
const (
	// ForkContextNativeTurn is the agent's own copy, cut at a turn.
	ForkContextNativeTurn SessionForkContextMode = "native_turn"
	// ForkContextNativeCurrent is the agent's own copy of its current state.
	ForkContextNativeCurrent SessionForkContextMode = "native_current"
	// ForkContextTerminalNative is a terminal agent's own fork command.
	ForkContextTerminalNative SessionForkContextMode = "terminal_native"
	// ForkContextTextHandoff is a fresh agent given the conversation as text,
	// for an agent that can't copy its own.
	ForkContextTextHandoff SessionForkContextMode = "text_handoff"
)

// SessionFork links a forked session to the session it was copied from.
type SessionFork struct {
	ForkSessionID   SessionID
	SourceSessionID SessionID
	// RootSessionID is the first session in a chain of forks. Fork numbers
	// ("Fork 2 of ...") count against it.
	RootSessionID SessionID
	SourceTurnID  string
	// SourceTitle is the source's title when the fork was made, so the marker
	// can still name it after the source is deleted.
	SourceTitle string
	ContextMode SessionForkContextMode
	ForkNumber  int
	CreatedAt   time.Time
}
