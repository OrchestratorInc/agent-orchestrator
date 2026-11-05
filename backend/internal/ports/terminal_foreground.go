package ports

import (
	"context"
	"errors"
)

// TerminalForeground is a factual, ephemeral reading of the program that owns
// a terminal: its foreground process group, that group leader's command line
// (capped), and whether it has been drawing output recently. It names no
// harness; callers classify Argv themselves.
type TerminalForeground struct {
	PGID int
	Argv []string
	Busy bool
}

// ErrTerminalForegroundUnavailable reports a runtime or terminal host that
// cannot name its foreground program (tmux handles, Windows ConPTY, or a
// detached host started by an older AO).
var ErrTerminalForegroundUnavailable = errors.New("terminal foreground is unavailable")

// TerminalForegroundWatcher is an optional runtime capability. Watch blocks,
// calling fn with the current foreground and again on every change, until ctx
// ends or the connection to the terminal host is lost.
type TerminalForegroundWatcher interface {
	WatchTerminalForeground(ctx context.Context, handle RuntimeHandle, fn func(TerminalForeground)) error
}
