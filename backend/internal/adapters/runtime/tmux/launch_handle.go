package tmux

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LaunchHandles identifies the fallback slot without treating its name as ownership proof.
func (r *Runtime) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	name, err := tmuxSessionName(id)
	if err != nil {
		return nil, err
	}
	return []ports.RuntimeHandle{{ID: name}}, nil
}

var _ ports.RuntimeLaunchHandleResolver = (*Runtime)(nil)
