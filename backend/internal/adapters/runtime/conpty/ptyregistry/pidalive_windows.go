//go:build windows

package ptyregistry

import (
	"golang.org/x/sys/windows"
)

func defaultPidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		_ = windows.CloseHandle(h)
		return true
	}
	return err == windows.ERROR_ACCESS_DENIED
}
