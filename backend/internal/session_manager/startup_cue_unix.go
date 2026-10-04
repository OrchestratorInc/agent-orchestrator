//go:build !windows

package sessionmanager

import (
	"os/exec"
	"syscall"
)

func configureWorkspaceCommandProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
