//go:build !darwin && !linux && !windows

package processalive

import (
	"context"
	"time"
)

func identify(ctx context.Context, pid int) Identity {
	startedAt := processStartedAt(ctx, pid)
	if startedAt.IsZero() {
		return Identity{}
	}
	return Identity{StartID: "ps:" + startedAt.Format(time.RFC3339), StartedAt: startedAt}
}
