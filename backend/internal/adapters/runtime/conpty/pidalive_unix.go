//go:build !windows

package conpty

import (
	"errors"
	"os"
	"syscall"
)

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

func defaultOSProcessFinder(pid int) (processKiller, error) {
	return os.FindProcess(pid)
}
