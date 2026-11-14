package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (m *Manager) checkSessionHealth(ctx context.Context, rec domain.SessionRecord) error {
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady || rec.NeedsImportResume() {
		return nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	if legacyInterruptedClientSetup(rec) {
		settled, current, err := m.rollbackLegacyClientSetup(ctx, rec)
		if err != nil || settled {
			return err
		}
		rec = current
	}
	if rec.Metadata.WorkspacePath == "" ||
		(rec.Metadata.Branch == "" && projectKindForSession(project, rec.ProjectID) != domain.ProjectKindScratch) {
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if m.chat == nil {
			return ports.ErrChatUnsupported
		}
		if m.chat.HasLiveChatController(rec.ID) {
			return nil
		}
		if rec.Metadata.ProviderConversationID == "" {
			return fmt.Errorf("check session %s: %w", rec.ID, ErrIncompleteHandle)
		}
		_, err = m.resumeChatController(ctx, "check session", rec, project,
			workspaceInfo(rec), false, true, "", domain.SessionInterfaceTransitionHistoryStrict)
		if errors.Is(err, ports.ErrChatHostNotRunning) {
			return m.recordAgentExited(ctx, rec)
		}
		return err
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return m.recordTerminalStoppedAtStartup(ctx, rec)
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("check session %s: %w", rec.ID, err)
	}
	if alive {
		return nil
	}
	return m.recordTerminalStoppedAtStartup(ctx, rec)
}

func (m *Manager) recordTerminalStoppedAtStartup(ctx context.Context, rec domain.SessionRecord) error {
	if err := m.releaseTerminalRuntime(ctx, rec); err != nil {
		return fmt.Errorf("check session %s: %w", rec.ID, err)
	}
	return m.recordAgentExited(ctx, rec)
}

var defaultStartupChatRetryDelays = []time.Duration{
	2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
	30 * time.Second, time.Minute, time.Minute,
}

func retryableStartupChatCheck(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatRecoveryInconclusive)
}

func (m *Manager) retryStartupChatChecks(ctx context.Context, ids []domain.SessionID) {
	for i, delay := range m.startupChatRetryDelays {
		if len(ids) == 0 {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		final := i == len(m.startupChatRetryDelays)-1
		remaining := ids[:0]
		for _, id := range ids {
			if m.retryStartupChatCheck(ctx, id, final) {
				remaining = append(remaining, id)
			}
		}
		ids = remaining
	}
}

func (m *Manager) retryStartupChatCheck(ctx context.Context, id domain.SessionID, final bool) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		if !final && ctx.Err() == nil {
			return true
		}
		m.finishStatusRecovery(ctx, domain.SessionRecord{ID: id}, err)
		return false
	}
	if !found || rec.IsTerminated || rec.HibernatedAt != nil ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || m.chat == nil ||
		m.chat.HasLiveChatController(id) {
		m.finishStatusRecovery(ctx, rec, nil)
		return false
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationReconcile); err != nil {
		if errors.Is(err, errAgentOperationInProgress) && !final {
			return true
		}
		m.finishStatusRecovery(ctx, rec, err)
		return false
	}
	err = func() error {
		defer m.endAgentOperation(id, agentOperationReconcile)
		recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
		defer cancel()
		return m.checkSessionHealth(recoveryCtx, rec)
	}()
	if err == nil {
		m.finishStatusRecovery(ctx, rec, nil)
		m.logger.Info("reconcile: chat reattached on retry", "sessionID", id)
		return false
	}
	retry := retryableStartupChatCheck(err) && !final && ctx.Err() == nil
	if !retry {
		m.finishStatusRecovery(ctx, rec, err)
	}
	m.logger.Warn("reconcile: chat reattach retry failed", "sessionID", id, "retrying", retry, "error", err)
	return retry
}

func legacyInterruptedClientSetup(rec domain.SessionRecord) bool {
	return rec.ClientRequestID != "" && !rec.ClientRequestCommitted && !rec.IsTerminated &&
		rec.Metadata.RuntimeHandleID == "" && rec.Metadata.ProviderConversationID == "" &&
		slices.ContainsFunc(rec.ProvisionSteps, func(step domain.SessionProvisionStep) bool {
			return step.ID == domain.SessionProvisionStepSetup && step.Status == domain.SessionProvisionStepRunning
		})
}

func (m *Manager) rollbackLegacyClientSetup(ctx context.Context, rec domain.SessionRecord) (bool, domain.SessionRecord, error) {
	release := sync.OnceFunc(m.acquireWorkspaceGate(rec.ProjectID))
	defer release()
	ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, release)
	current, found, err := m.store.GetSession(ctx, rec.ID)
	if err != nil || !found {
		return true, current, err
	}
	if !current.CreatedAt.Equal(rec.CreatedAt) || !legacyInterruptedClientSetup(current) {
		return false, current, nil
	}
	rows, isProject, err := m.workspaceProjectRows(ctx, current)
	if err != nil {
		return true, current, err
	}
	var info *ports.WorkspaceProjectInfo
	if isProject {
		info = &ports.WorkspaceProjectInfo{Root: workspaceInfo(current), Worktrees: rows}
	}
	m.clearProvisionedWorkspace(ctx, current.ID, current.Metadata.WorkspacePath)
	m.rollbackSeedSpawnWorkspace(ctx, current, workspaceInfo(current), info, false, false)
	return true, current, nil
}
