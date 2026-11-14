//go:build windows

package cli

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
