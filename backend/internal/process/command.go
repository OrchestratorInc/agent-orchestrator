package process

import (
	"context"
	"os/exec"
)

// Command creates a non-interactive child process. On Windows it suppresses
// transient console windows for CLI tools launched by the desktop daemon, starts
// a batch shim's target directly when it can follow the shim, and otherwise runs
// the batch file through cmd.exe /d /s /c (see rewriteBatchShim).
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	rewriteBatchShim(cmd)
	configureHidden(cmd)
	return cmd
}

// CommandContext is Command with cancellation support.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Callers intentionally select argv; project hooks execute user-authored shell commands.
	rewriteBatchShim(cmd)
	configureHidden(cmd)
	return cmd
}

// ConfigureTreeCancellation makes a context cancellation stop the command's
// process group on Unix or its child tree on Windows. Call before Start.
// The command must be created with CommandContext.
func ConfigureTreeCancellation(cmd *exec.Cmd) {
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
}
