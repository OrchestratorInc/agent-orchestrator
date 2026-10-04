//go:build windows

package sessionmanager

import (
	"os/exec"
	"strconv"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureWorkspaceCommandProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return aoprocess.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	}
}
