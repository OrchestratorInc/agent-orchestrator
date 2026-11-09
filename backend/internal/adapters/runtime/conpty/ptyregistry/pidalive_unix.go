//go:build !windows

package ptyregistry

import (
	"errors"
	"syscall"
)

// defaultPidAlive probes PID liveness via signal 0. nil and EPERM both mean
// alive (process exists but may not be queryable). ESRCH means dead.
// Mirrors process.kill(pid, 0) with EPERM-means-alive from the TS source.
func defaultPidAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	return errors.Is(err, syscall.EPERM), nil
}
