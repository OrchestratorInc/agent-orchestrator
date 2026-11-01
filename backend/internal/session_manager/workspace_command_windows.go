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
	// cmd.exe needs shell text, not Go's Windows argv escaping. /s removes
	// the enclosing quotes even when the command starts with a quoted path.
	cmd.SysProcAttr.CmdLine = syscall.EscapeArg(cmd.Path) + ` /d /s /c "` + command + `"`
	return cmd
}
