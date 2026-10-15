//go:build !windows

package litmus

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd as the leader of a new process group, so one kill reaches its children.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func sigkill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

func sigkillGroup(pgid int) { _ = syscall.Kill(-pgid, syscall.SIGKILL) }
