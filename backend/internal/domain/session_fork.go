package domain

import "time"

type SessionForkContextMode string

const (
	ForkContextNativeTurn     SessionForkContextMode = "native_turn"
	ForkContextNativeCurrent  SessionForkContextMode = "native_current"
	ForkContextTerminalNative SessionForkContextMode = "terminal_native"
	ForkContextTextHandoff    SessionForkContextMode = "text_handoff"
)

type SessionFork struct {
	ForkSessionID   SessionID
	SourceSessionID SessionID
	RootSessionID   SessionID
	SourceTurnID    string
	SourceTitle     string
	ContextMode     SessionForkContextMode
	ForkNumber      int
	CreatedAt       time.Time
}
