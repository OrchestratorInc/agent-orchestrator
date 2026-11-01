//go:build !windows

package sessionmanager

import (
	"context"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func workspaceStepCommand(ctx context.Context, command string) *exec.Cmd {
	return aoprocess.CommandContext(ctx, "sh", "-c", command)
}
