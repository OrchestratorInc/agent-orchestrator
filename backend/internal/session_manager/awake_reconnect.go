package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReconnectAwakeAgent can only attach to an existing native chat provider. It
// cannot restore a worktree, start a replacement process, send a prompt, or
// resume an intentionally stopped/hibernated session. The normal per-session
// mutation and controller-owner fences still protect user changes.
func (m *Manager) ReconnectAwakeAgent(ctx context.Context, target domain.AwakeAgentTarget) error {
	if err := m.beginAgentResume(ctx, target.SessionID); err != nil {
		return err
	}
	defer m.endAgentResume(target.SessionID)
	rec, found, err := m.store.GetSession(ctx, target.SessionID)
	if err != nil {
		return err
	}
	if !found || rec.IsTerminated || rec.HibernatedAt != nil || rec.ProvisionState.WithDefault() != domain.SessionProvisionReady || rec.ControllerOwner() != target.Owner || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || rec.Metadata.ProviderConversationID == "" || rec.Metadata.ControllerGeneration == "" {
		return fmt.Errorf("awake reconnect: session ownership changed or is not eligible")
	}
	if active, err := m.hasActiveInterfaceTransition(ctx, rec.ID); err != nil {
		return err
	} else if active {
		return ErrInterfaceTransitionInProgress
	}
	if m.chat == nil {
		return ports.ErrChatUnsupported
	}
	if m.chat.HasLiveChatController(rec.ID) {
		return nil
	}
	release, err := m.beginHarnessUse(rec.Harness)
	if err != nil {
		return err
	}
	defer release()
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	_, err = m.resumeChatController(ctx, "awake reconnect", rec, project, workspaceInfo(rec), false, true, "", domain.SessionInterfaceTransitionHistoryStrict, target)
	return err
}
