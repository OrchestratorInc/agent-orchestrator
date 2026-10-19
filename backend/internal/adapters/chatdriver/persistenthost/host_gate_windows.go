//go:build windows

package persistenthost

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// The gate file must remain in place so every host locks the same file.
func acquireHostGate(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // Caller supplies a validated AO-owned host path.
	if err != nil {
		return nil, fmt.Errorf("open chat host gate: %w", err)
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrHostExists
		}
		return nil, fmt.Errorf("lock chat host gate: %w", err)
	}
	return func() { _ = file.Close() }, nil
}
