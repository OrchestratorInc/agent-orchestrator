//go:build !windows

package systemexec

import (
	"context"
	"os/exec"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	return aoprocess.CommandContext(ctx, name, args...), nil //nolint:gosec // Callers supply server-owned argv.
}

func refreshExecutablePath() {}
