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

// checkSessionHealth observes existing controllers. Startup must never create a
// provider, restore a worktree, or replay a task to discover whether it is alive.
func (m *Manager) checkSessionHealth(ctx context.Context, rec domain.SessionRecord) error {
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
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
	// Preserve cleanup of interrupted spawns that never committed a workspace.
	// Their client request IDs must remain retryable after a daemon crash.
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
		return m.recordAgentExited(ctx, rec)
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("check session %s: %w", rec.ID, err)
	}
	if alive {
		// The start hook may fail without stopping the TUI. Keep a live runtime
		// intact; its unverified identity will not qualify for native restore.
		return nil
	}
	return m.recordAgentExited(ctx, rec)
}

// defaultStartupChatRetryDelays spaces the reattach attempts for a Chat whose
// startup check failed transiently. Its provider host is still running, so a
// later attempt reconnects to it: no provider is launched and no history moves.
var defaultStartupChatRetryDelays = []time.Duration{
	2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second,
	30 * time.Second, time.Minute, time.Minute,
}

// retryableStartupChatCheck reports failures a later reattach can succeed past:
// the check's deadline (a busy writer at startup) and an inconclusive host owner
// (a replaced daemon has not detached yet). Anything else needs the user.
func retryableStartupChatCheck(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ports.ErrChatRecoveryInconclusive)
}

// retryStartupChatChecks reattaches Chats whose startup check failed
// transiently. Without it, a live host reads "stopped" until the user clicks
// Resume, even though the next attempt would reconnect immediately. The
// sessions stay "checking" until an attempt settles them, so clients show a
// reconnect in progress instead of offering Resume.
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

// retryStartupChatCheck makes one reattach attempt and reports whether the
// session still needs another one. Every other outcome settles its readiness.
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
		// Gone, asleep, or already reattached (for example by Resume).
		m.finishStatusRecovery(ctx, rec, nil)
		return false
	}
	if err := m.beginAgentOperation(ctx, id, agentOperationReconcile); err != nil {
		// A user operation owns the session now; look again after it ends.
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

// legacyInterruptedClientSetup matches a synchronous client start that a build
// from before background branch naming checkpointed setup for and then lost to
// a restart. Nothing creates this state any more; such a start was never
// acknowledged, so it is rolled back to keep its client request retryable.
// ponytail: upgrade path for older builds; delete once they are no longer in use.
func legacyInterruptedClientSetup(rec domain.SessionRecord) bool {
	return rec.ClientRequestID != "" && !rec.ClientRequestCommitted && !rec.IsTerminated &&
		rec.Metadata.RuntimeHandleID == "" && rec.Metadata.ProviderConversationID == "" &&
		slices.ContainsFunc(rec.ProvisionSteps, func(step domain.SessionProvisionStep) bool {
			return step.ID == domain.SessionProvisionStepSetup && step.Status == domain.SessionProvisionStepRunning
		})
}

// rollbackLegacyClientSetup reports settled when the session is gone or was
// rolled back; otherwise it returns the current record for the normal check.
func (m *Manager) rollbackLegacyClientSetup(ctx context.Context, rec domain.SessionRecord) (bool, domain.SessionRecord, error) {
	release := sync.OnceFunc(m.acquireWorkspaceGate(rec.ProjectID))
	defer release()
	ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, release)
	current, found, err := m.store.GetSession(ctx, rec.ID)
	if err != nil || !found {
		return true, current, err
	}
	// An initial spawn can finish while this pass waits for the project gate.
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
	// The old build saved this unacknowledged path; clear it so the seed row
	// can be deleted once the worktree is gone.
	m.clearProvisionedWorkspace(ctx, current.ID, current.Metadata.WorkspacePath)
	m.rollbackSeedSpawnWorkspace(ctx, current, workspaceInfo(current), info, false, false)
	return true, current, nil
}
