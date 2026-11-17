//go:build windows

// Package processalive probes whether an operating-system process id still
// maps to a live process.
package processalive

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Alive reports whether pid exists. Access denied counts as alive: the process
// exists even if the current user cannot wait on it.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}
	return status == uint32(windows.WAIT_TIMEOUT)
}

// Exited reports that pid is proven gone, as for Node's process.kill(pid, 0):
// no such process, or one that has already finished. Access denied means it
// still exists.
func Exited(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && status == uint32(windows.WAIT_OBJECT_0)
}
