//go:build !windows

package persistenthost

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// The gate file must remain in place: unlinking it lets another host lock a
// different inode while the current owner still holds the old one.
func acquireHostGate(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // Caller supplies a validated AO-owned host path.
	if err != nil {
		return nil, fmt.Errorf("open chat host gate: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrHostExists
		}
		return nil, fmt.Errorf("lock chat host gate: %w", err)
	}
	return func() { _ = file.Close() }, nil
}
