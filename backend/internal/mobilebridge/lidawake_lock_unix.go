//go:build darwin || linux

package mobilebridge

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func lockWakeSetup(ctx context.Context, path string) (func(), error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Close(fd) }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = syscall.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = syscall.Close(fd)
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
