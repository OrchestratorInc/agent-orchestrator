package ports

import (
	"context"
	"errors"
)

// TerminalForeground is a reading of the program that owns a terminal: its
// process group, the group leader's argv (capped), and whether it is drawing.
type TerminalForeground struct {
	PGID int
	Argv []string
	Busy bool
	// EchoOnly reports a quiet-screen update caused only by typed-input echo,
	// so a caller can keep its last screen reading instead of reading again.
	EchoOnly bool
}

// ErrTerminalForegroundUnavailable reports a terminal that cannot name its
// foreground program: tmux, Windows ConPTY, or a host from an older AO.
var ErrTerminalForegroundUnavailable = errors.New("terminal foreground is unavailable")

// TerminalForegroundWatcher is an optional runtime capability. It blocks,
// calling fn with the current reading and each change, until ctx ends or the
// host connection drops.
type TerminalForegroundWatcher interface {
	WatchTerminalForeground(ctx context.Context, handle RuntimeHandle, fn func(TerminalForeground)) error
}
