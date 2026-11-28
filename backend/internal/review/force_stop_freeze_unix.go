//go:build !windows

package review

import "syscall"

// freezeProcess stops a process with SIGSTOP. A stopped process cannot spawn
// anything, and SIGKILL still ends it.
func freezeProcess(pid int) error { return syscall.Kill(pid, syscall.SIGSTOP) }
