package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// errWorkloadUninspectable means the runtime cannot count the commands a
// terminal runs, so a caller cannot prove the terminal is quiet.
var errWorkloadUninspectable = errors.New("runtime cannot inspect the commands an agent is running")

// RunningWorkloadCommands counts the commands id's agent is running in its
// terminal: a test run, a build, a dev server (see
// ports.WorkloadCommandInspector). Only a live TUI session runs commands in a
// terminal AO can inspect; any other session reports none. An error means the
// count could not be proved.
func (m *Manager) RunningWorkloadCommands(ctx context.Context, id domain.SessionID) (int, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("running workload commands %s: %w", id, err)
	}
	if !ok {
		return 0, fmt.Errorf("running workload commands %s: %w", id, ErrNotFound)
	}
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return 0, nil
	}
	inspector, ok := m.runtime.(ports.WorkloadCommandInspector)
	if !ok {
		return 0, errWorkloadUninspectable
	}
	return inspector.RunningWorkloadCommands(ctx, runtimeHandle(rec.Metadata), ports.SupervisedProcessRef{
		SessionID: rec.ID, LaunchID: rec.Metadata.RuntimeLaunchID,
	})
}
