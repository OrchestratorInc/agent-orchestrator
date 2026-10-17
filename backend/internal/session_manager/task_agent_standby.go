package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TaskAgentPrewarm is the agent, model, and approval choice a new-chat page
// holds for its task preparation, as the later delegate request will send it.
type TaskAgentPrewarm struct {
	Harness        domain.AgentHarness
	Model          string
	Effort         string
	EffortOverride bool
	Permissions    ports.PermissionMode
}

// PrewarmTaskAgent starts the agent a new-chat page has selected in its task
// preparation's worktree, under the session id the preparation reserved, so
// the send that claims the preparation adopts a running provider. A new pick
// replaces the earlier standby. Agents without standby support are a no-op.
func (m *Manager) PrewarmTaskAgent(ctx context.Context, token domain.TaskPreparationToken, in TaskAgentPrewarm) error {
	token = domain.TaskPreparationToken(strings.TrimSpace(string(token)))
	m.taskPreparationsMu.Lock()
	prep := m.taskPreparations[token]
	if prep != nil && (prep.cancelled || prep.cleaning) {
		prep = nil
	}
	m.taskPreparationsMu.Unlock()
	if prep == nil {
		return nil
	}
	id := prep.record.ID
	lock := m.chatStandbyLock(id)
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-lock }()
	// A send may have claimed the preparation while this waited for the lock;
	// its launch then owns the worktree and starts its own agent.
	m.taskPreparationsMu.Lock()
	unclaimed := m.taskPreparations[token] == prep && !prep.cancelled && !prep.cleaning
	m.taskPreparationsMu.Unlock()
	if !unclaimed {
		return nil
	}
	// The preparation's worktree is still its own, so an earlier pick's agent
	// files can go with it.
	m.dropChatStandby(id, true)
	epoch := m.chatStandbyEpoch(id)

	ws, workspaceProject, err := m.awaitTaskPreparation(ctx, prep)
	if err != nil || ws.Path == "" {
		return err
	}
	project := prep.project
	cfg := ports.SpawnConfig{
		ProjectID: domain.ProjectID(project.ID),
		Kind:      domain.KindWorker,
		Harness:   in.Harness,
		AgentConfig: ports.AgentConfig{
			Model:       strings.TrimSpace(in.Model),
			Effort:      strings.TrimSpace(in.Effort),
			Permissions: in.Permissions,
		},
		EffortOverride: in.EffortOverride,
		RequestedMode:  domain.SessionModeChat,
	}
	// Resolve exactly as Spawn does for a Chat worker.
	cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)
	if cfg.Harness == "" || !m.supportsChatStandby(cfg.Harness) {
		return nil
	}
	agentConfig, err := m.resolveAgentConfig(ctx, cfg, project.Config)
	if err != nil {
		return fmt.Errorf("prewarm task agent %s: %w", id, err)
	}
	systemPrompt, err := m.buildSystemPrompt(ctx, cfg.Kind, cfg.ProjectID, id, cfg.RequestedOutput, true)
	if err != nil {
		return fmt.Errorf("prewarm task agent %s: system prompt: %w", id, err)
	}
	err = m.startChatStandby(ctx, chatStandbyStart{
		record: prep.record, project: project, target: cfg.Harness, agentConfig: agentConfig,
		systemPrompt: systemPrompt, workspace: ws.Path,
		additionalDirectories: workspaceProjectDirectories(ws.Path, workspaceProject),
		ttl:                   m.taskPreparationTTL,
		epoch:                 epoch,
		// Once a send claims the preparation the worktree is the session's, and
		// its agent files are the launch's to keep.
		keepWorkspaceOnExpiry: true,
		abandoned: func() bool {
			m.taskPreparationsMu.Lock()
			defer m.taskPreparationsMu.Unlock()
			return prep.cancelled || prep.cleaning
		},
	})
	if err != nil {
		return fmt.Errorf("prewarm task agent %s: %w", id, err)
	}
	return nil
}

// discardSessionStandby drops whatever standby a session id holds, waiting for
// a prewarm still starting one. Workspace files are left to the caller.
func (m *Manager) discardSessionStandby(ctx context.Context, id domain.SessionID) {
	m.chatStandbyMu.Lock()
	_, held := m.chatStandbyLocks[id]
	m.chatStandbyMu.Unlock()
	if !held {
		return
	}
	lock := m.chatStandbyLock(id)
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-lock }()
	m.dropChatStandby(id, false)
}
