package process

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// Command creates a non-interactive child process. On Windows it suppresses
// transient console windows for CLI tools launched by the desktop daemon.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureHidden(cmd)
	return cmd
}

// CommandContext is Command with cancellation support.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Callers intentionally select argv; project hooks execute user-authored shell commands.
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

// InterruptOnCancel makes a context cancellation send os.Interrupt and kill
// only after grace, so the child can run exit handlers such as releasing lock
// files. Windows cannot deliver os.Interrupt and kills immediately. Call
// before Start; the command must be created with CommandContext.
func InterruptOnCancel(cmd *exec.Cmd, grace time.Duration) {
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = grace
}
