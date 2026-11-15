package ports

import (
	"context"
	"errors"
)

var ErrTerminalTitleUnavailable = errors.New("terminal title is unavailable")

type TerminalTitleWatcher interface {
	WatchTerminalTitle(ctx context.Context, handle RuntimeHandle, fn func(title string)) error
}
