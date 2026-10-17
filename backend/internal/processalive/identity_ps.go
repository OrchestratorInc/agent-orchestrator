//go:build !darwin && !windows

package processalive

import (
	"context"
	"time"
)

func processStartedAt(ctx context.Context, pid int) time.Time {
	start, err := time.Parse(time.ANSIC, psStartTime(ctx, pid, []string{"LC_ALL=C", "TZ=UTC"}))
	if err != nil {
		return time.Time{}
	}
	return start.UTC()
}
