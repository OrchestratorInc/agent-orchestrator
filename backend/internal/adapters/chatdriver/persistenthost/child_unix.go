//go:build !windows

package persistenthost

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureProviderProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func startProviderProcess(cmd *exec.Cmd) (func(), error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {}, nil
}

// killProviderProcess asks the provider's process group to exit before it
// kills it, so the agent can stop the commands it runs in groups of their own.
func killProviderProcess(_ context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return aoprocess.TerminateGroup(cmd.Process.Pid, 2*time.Second)
}
