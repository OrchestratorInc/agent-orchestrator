//go:build windows

package runfile

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lock(path string) (func(), error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // AO-owned run-file path.
	if err != nil {
		return nil, fmt.Errorf("open run-file lock: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock run-file (another startup or takeover may be active): %w", err)
	}
	return func() { _ = file.Close() }, nil
}
