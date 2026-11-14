package sessionmanager

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const statusVerificationLimit = 30 * time.Second

type statusRecovery struct {
	pending bool
	failed  *domain.SessionRecord
}

func (m *Manager) StatusRecoveryRevision() uint64 {
	m.statusRecoveryMu.RLock()
	defer m.statusRecoveryMu.RUnlock()
	return m.statusRecoveryRevision
}

func (m *Manager) SessionStatusReadiness(rec domain.SessionRecord) string {
	if rec.NeedsImportResume() {
		return "ready"
	}
	m.statusRecoveryMu.RLock()
	defer m.statusRecoveryMu.RUnlock()
	result, found := m.statusRecoveries[rec.ID]
	if found {
		if result.pending {
			return "checking"
		}
		if result.failed != nil && !rec.IsTerminated && rec.Activity.State != domain.ActivityExited &&
			rec.ControllerOwner() == result.failed.ControllerOwner() && rec.Activity == result.failed.Activity {
			return "unavailable"
		}
		return "ready"
	}
	select {
	case <-m.startupBackgroundReconcileDone:
		if m.statusRecoveryFailed {
			return "unavailable"
		}
		return "ready"
	default:
	}
	return "checking"
}

func (m *Manager) beginStatusRecovery(id domain.SessionID) {
	m.statusRecoveryMu.Lock()
	defer m.statusRecoveryMu.Unlock()
	m.statusRecoveries[id] = statusRecovery{pending: true}
	m.statusRecoveryRevision++
}

func (m *Manager) markFreshSessionStatusReady(id domain.SessionID) {
	m.statusRecoveryMu.Lock()
	defer m.statusRecoveryMu.Unlock()
	select {
	case <-m.startupBackgroundReconcileDone:
		if !m.statusRecoveryFailed {
			return
		}
	default:
	}
	if result, found := m.statusRecoveries[id]; found && result.pending {
		return
	}
	m.statusRecoveries[id] = statusRecovery{}
	m.statusRecoveryRevision++
}

func (m *Manager) finishStatusRecovery(ctx context.Context, before domain.SessionRecord, recoveryErr error) {
	result := statusRecovery{}
	if recoveryErr != nil {
		current, found, err := m.store.GetSession(ctx, before.ID)
		if err == nil && found {
			before = current
		}
		result.failed = &before
	}
	m.statusRecoveryMu.Lock()
	m.statusRecoveries[before.ID] = result
	m.statusRecoveryRevision++
	m.statusRecoveryMu.Unlock()
}
