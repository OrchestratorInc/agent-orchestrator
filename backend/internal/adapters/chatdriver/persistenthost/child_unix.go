//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"os"
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

func killProviderProcess(_ context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return aoprocess.TerminateGroup(cmd.Process.Pid, 2*time.Second)
}

func terminateProviderProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func openHostFile(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // AO-owned path derived from validated session id.
}

func hostFileBusy(error) bool { return false }
