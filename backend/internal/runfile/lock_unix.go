//go:build !windows

package runfile

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Never unlink the lock file: every writer must lock the same inode.
func lock(path string) (func(), error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // AO-owned run-file path.
	if err != nil {
		return nil, fmt.Errorf("open run-file lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock run-file (another startup or takeover may be active): %w", err)
	}
	return func() { _ = file.Close() }, nil
}
