package process

import (
	"context"
	"os"
	"os/exec"
	"time"
)

func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureHidden(cmd)
	return cmd
}

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Callers intentionally select argv; project hooks execute user-authored shell commands.
	configureHidden(cmd)
	return cmd
}

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
