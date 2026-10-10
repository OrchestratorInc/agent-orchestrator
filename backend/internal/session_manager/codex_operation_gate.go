package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// acquireCodexControllerAdmission remains a narrow compatibility seam for
// older session flows. Account selection is now handled per request by the
// managed provider bridge, so no daemon-wide credential lock is required.
func (m *Manager) acquireCodexControllerAdmission(context.Context, domain.AgentHarness) (func(), error) {
	return func() {}, nil
}
