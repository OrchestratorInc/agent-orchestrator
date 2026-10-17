//go:build !windows

package conpty

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive probes PID liveness via signal 0. nil and EPERM both mean alive
// (process exists but may not be signallable). ESRCH means dead.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// defaultOSProcessFinder wraps os.FindProcess for Unix (always succeeds on
// Unix; the returned handle is valid for Kill).
func defaultOSProcessFinder(pid int) (processKiller, error) {
	return os.FindProcess(pid)
}
