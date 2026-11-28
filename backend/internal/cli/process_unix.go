//go:build !windows

package cli

import "syscall"

// detachSysProcAttr puts the desktop app in a new session (Setsid) so it is no
// longer in the launcher's foreground process group and won't receive the
// terminal's SIGINT/SIGHUP.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
