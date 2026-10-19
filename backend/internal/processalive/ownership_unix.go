//go:build !windows && !linux

package processalive

import (
	"context"
	"errors"
	"syscall"
)

func processGone(ctx context.Context, pid int) bool {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return isZombieContext(ctx, pid)
}
