package runtimeselect

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.RuntimeLaunchHandleResolver = (*hybridRuntime)(nil)

func (r *hybridRuntime) LaunchHandles(id domain.SessionID) ([]ports.RuntimeHandle, error) {
	var handles []ports.RuntimeHandle
	seen := make(map[string]bool)
	for _, route := range []struct {
		backend routedBackend
		prefix  string
	}{{r.direct, directHandlePrefix}, {r.legacy, ""}} {
		resolver, ok := route.backend.(ports.RuntimeLaunchHandleResolver)
		if !ok {
			return nil, errors.New("runtime: backend does not expose launch identities")
		}
		raw, err := resolver.LaunchHandles(id)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, errors.New("runtime: backend has no launch identities")
		}
		for _, handle := range raw {
			if !validBackendLaunchID(handle.ID) {
				return nil, errors.New("runtime: invalid backend launch identity")
			}
			handle.ID = route.prefix + handle.ID
			if seen[handle.ID] {
				return nil, errors.New("runtime: ambiguous launch identity")
			}
			seen[handle.ID] = true
			handles = append(handles, handle)
		}
	}
	return handles, nil
}

func validBackendLaunchID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
