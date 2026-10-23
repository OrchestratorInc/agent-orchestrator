//go:build darwin || linux

package conpty

import (
	"context"
	"fmt"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// snapshotManagedChildren lists pid, parent pid, and kernel state for every
// process. Both macOS and Linux ps support these columns; no command column is
// requested, so no argv text can be mistaken for process state.
func snapshotManagedChildren(ctx context.Context, supervisorPID int) (managedChildren, error) {
	out, err := aoprocess.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,stat=").Output()
	if err != nil {
		return managedChildren{}, fmt.Errorf("conpty: ps: %w", err)
	}
	return parseManagedChildren(string(out), supervisorPID)
}
