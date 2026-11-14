//go:build windows

package sessionmanager

import (
	"context"
	"os/exec"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func workspaceStepCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := aoprocess.CommandContext(ctx, "cmd")
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Path) + ` /d /s /c "` + command + `"`
	return cmd
}
