//go:build windows

package cli

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachSysProcAttr starts the desktop app in a new process group so it does not
// receive the console's CTRL_C/CTRL_BREAK from the terminal that ran `ao start`.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
