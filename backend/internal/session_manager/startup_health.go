package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

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
	if interruptedClientSetup(rec) {
		release := sync.OnceFunc(m.acquireWorkspaceGate(rec.ProjectID))
		defer release()
		ctx = context.WithValue(ctx, spawnWorkspaceGateKey{}, release)
		current, found, err := m.store.GetSession(ctx, rec.ID)
		if err != nil || !found {
			return err
		}
		// An initial spawn can finish while this pass waits for the project gate.
		if current.CreatedAt.Equal(rec.CreatedAt) && interruptedClientSetup(current) {
			var info *ports.WorkspaceProjectInfo
			if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
				rows, _, err := m.workspaceProjectRows(ctx, current)
				if err != nil {
					return err
				}
				info = &ports.WorkspaceProjectInfo{Root: workspaceInfo(current), Worktrees: rows}
			}
			m.rollbackSeedSpawnWorkspace(ctx, current, workspaceInfo(current), info, false, false)
			return nil
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
		return nil
	}
	return m.recordAgentExited(ctx, rec)
}

// Setup's durable completion is written before any controller may launch.
func interruptedClientSetup(rec domain.SessionRecord) bool {
	return rec.ClientRequestID != "" && !rec.ClientRequestCommitted && !rec.IsTerminated &&
		rec.Metadata.RuntimeHandleID == "" && rec.Metadata.ProviderConversationID == "" &&
		slices.ContainsFunc(rec.ProvisionSteps, func(step domain.SessionProvisionStep) bool {
			return step.ID == domain.SessionProvisionStepSetup && step.Status == domain.SessionProvisionStepRunning
		})
}
