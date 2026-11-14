//go:build !windows

package acp

import (
	"os/exec"
	"syscall"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return aoprocess.TerminateGroup(cmd.Process.Pid, 2*time.Second)
}
