package sessionmanager

import (
	"context"
	"fmt"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// roleAgentStore is the store's narrow writer for SessionMetadata.RoleAgent.
type roleAgentStore interface {
	SetSessionRoleAgent(ctx context.Context, id domain.SessionID, agent domain.AgentHarness) (bool, error)
}

// RecordAgentChoice notes that the session's agent was just chosen by switching
// it to another agent. It records the agent the project names for the
// session's role right now, so the switch never reads as the project's setting
// having drifted from the running session; only a later change to the setting
// does. A role with no agent records none, which never reads as drift.
func (m *Manager) RecordAgentChoice(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(roleAgentStore)
	if !ok {
		return nil
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	if _, err := store.SetSessionRoleAgent(ctx, id, roleOverride(rec.Kind, project.Config).Harness); err != nil {
		return fmt.Errorf("record agent choice for %s: %w", id, err)
	}
	return nil
}
