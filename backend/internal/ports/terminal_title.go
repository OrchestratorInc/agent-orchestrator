package ports

import (
	"context"
	"errors"
)

// ErrTerminalTitleUnavailable reports a terminal whose runtime cannot report
// titles: a legacy tmux handle or a host from an older protocol. Retrying the
// same handle cannot succeed.
var ErrTerminalTitleUnavailable = errors.New("terminal title is unavailable")

// TerminalTitleWatcher is an optional runtime capability. It blocks, calling fn
// with the terminal's current title and then each change, until ctx ends or the
// connection to the terminal is lost. An empty title means the program has set
// none or reset the one it had.
type TerminalTitleWatcher interface {
	WatchTerminalTitle(ctx context.Context, handle RuntimeHandle, fn func(title string)) error
}
