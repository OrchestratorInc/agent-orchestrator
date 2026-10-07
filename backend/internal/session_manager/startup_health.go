package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// checkSessionHealth observes existing controllers. Startup must never create a
// provider, restore a worktree, or replay a task to discover whether it is alive.
func (m *Manager) checkSessionHealth(ctx context.Context, rec domain.SessionRecord) error {
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return nil
	}
	if unfinishedWorkspaceSetup(rec) {
		// A synchronous request interrupted before controller commit can retry
		// only after safe removal. Dirty workspaces keep their ownership row.
		if !rec.IsTerminated && rec.Metadata.Prompt == "" &&
			rec.Metadata.RuntimeHandleID == "" && rec.Metadata.RuntimeLaunchID == "" &&
			rec.Metadata.ProviderConversationID == "" && rec.Metadata.ControllerGeneration == "" &&
			rec.Metadata.AgentSessionID == "" && rec.Metadata.AgentSessionIDLaunchID == "" {
			ws := workspaceInfo(rec)
			var workspaceProject *ports.WorkspaceProjectInfo
			if rows, ok, err := m.workspaceProjectRows(ctx, rec); err != nil {
				return err
			} else if ok {
				workspaceProject = &ports.WorkspaceProjectInfo{Root: ws, Worktrees: rows}
			}
			if m.destroySpawnWorkspace(ctx, ws, workspaceProject) {
				m.clearProvisionedWorkspace(ctx, rec.ID, ws.Path)
				m.rollbackSpawnSeedRow(ctx, rec.ID)
				return nil
			}
		}
		return fmt.Errorf("check session %s: workspace setup did not complete; preserve workspace for cleanup", rec.ID)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
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
