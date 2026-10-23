//go:build windows

package conpty

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// snapshotManagedChildren uses the Toolhelp process snapshot behind
// procmem.Snapshot. Toolhelp lists only live processes with their parent pid
// and executable name; no command text is parsed, so it carries none of the
// Unix ps text heuristics.
func snapshotManagedChildren(ctx context.Context, supervisorPID int) (managedChildren, error) {
	table, err := procmem.Snapshot(ctx, nil)
	if err != nil {
		return managedChildren{}, err
	}
	tree := table.Tree(supervisorPID)
	if len(tree.Processes) == 0 {
		return managedChildren{}, nil
	}
	got := managedChildren{supervisorListed: true}
	for _, p := range tree.Processes {
		if p.PPID == supervisorPID && p.PID != supervisorPID {
			got.running++
		}
	}
	return got, nil
}
