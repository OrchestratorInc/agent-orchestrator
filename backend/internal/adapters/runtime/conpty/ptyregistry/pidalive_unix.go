//go:build !windows

package ptyregistry

import (
	"errors"
	"syscall"
)

func defaultPidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
