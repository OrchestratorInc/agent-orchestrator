package sessionmanager

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// AccountsManagerSwitchCanRetry does not reserve or authorize a retry.
func (m *Manager) AccountsManagerSwitchCanRetry(op domain.AccountsManagerSwitch) bool {
	switch op.Phase {
	case domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchRecoveryRequired:
	default:
		return false
	}
	m.agentSwitchWorkerMu.Lock()
	closed := m.agentSwitchWorkersClosed
	m.agentSwitchWorkerMu.Unlock()
	if closed || m.backgroundContext.Err() != nil {
		return false
	}
	m.accountSwitchMu.Lock()
	defer m.accountSwitchMu.Unlock()
	run := m.accountSwitches[op.SessionID]
	return run != nil && run.id == op.ID && !run.running && !run.cancelled
}
