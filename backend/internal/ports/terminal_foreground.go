package ports

import (
	"context"
	"errors"
)

type TerminalForeground struct {
	PGID     int
	Argv     []string
	Busy     bool
	EchoOnly bool
}

var ErrTerminalForegroundUnavailable = errors.New("terminal foreground is unavailable")

type TerminalForegroundWatcher interface {
	WatchTerminalForeground(ctx context.Context, handle RuntimeHandle, fn func(TerminalForeground)) error
}
