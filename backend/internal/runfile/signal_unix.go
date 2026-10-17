//go:build !windows

package runfile

import "syscall"

func signalDaemon(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
