package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/codexops"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (m *Manager) acquireCodexControllerAdmission(ctx context.Context, harness domain.AgentHarness) (func(), error) {
	if harness != domain.HarnessCodex || m.codexOperationGate == nil {
		return func() {}, nil
	}
	return m.codexOperationGate.AcquireSharedWait(ctx)
}

func defaultCodexOperationGate(gate ports.CodexOperationGate) ports.CodexOperationGate {
	if gate != nil {
		return gate
	}
	return codexops.NewGate()
}
