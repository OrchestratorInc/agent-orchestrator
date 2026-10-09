//go:build !windows

package systemexec

import (
	"context"
	"os/exec"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, name, args...), nil //nolint:gosec // Callers supply server-owned argv.
}

func refreshExecutablePath() {}
