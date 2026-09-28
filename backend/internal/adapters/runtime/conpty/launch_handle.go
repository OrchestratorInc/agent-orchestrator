package conpty

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LaunchHandles identifies the native slot without probing or claiming runtime ownership.
func (r *Runtime) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	if !validSessionID.MatchString(string(id)) {
		return nil, errors.New("conpty: invalid session id")
	}
	return []ports.RuntimeHandle{{ID: string(id)}}, nil
}

var _ ports.RuntimeLaunchHandleResolver = (*Runtime)(nil)
