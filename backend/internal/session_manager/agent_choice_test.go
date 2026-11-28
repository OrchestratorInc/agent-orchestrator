package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type roleAgentRecordingStore struct {
	*fakeStore
	roleAgents map[domain.SessionID]domain.AgentHarness
}

func (s *roleAgentRecordingStore) SetSessionRoleAgent(_ context.Context, id domain.SessionID, agent domain.AgentHarness) (bool, error) {
	s.roleAgents[id] = agent
	return true, nil
}

// A spawn records the agent the project names for the role, even when the
// spawn picked another one: an explicit pick is a choice, not drift.
func TestSeedRecordRecordsTheProjectRoleAgent(t *testing.T) {
	cfg := domain.ProjectConfig{
		Worker:       domain.RoleOverride{Harness: domain.HarnessCodex},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex},
	}
	now := time.Date(2026, time.November, 26, 9, 0, 0, 0, time.UTC)
	orchestrator := seedRecord(ports.SpawnConfig{ProjectID: "p", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode}, cfg, now)
	if orchestrator.Metadata.RoleAgent != domain.HarnessCodex {
		t.Fatalf("orchestrator role agent = %q, want %q", orchestrator.Metadata.RoleAgent, domain.HarnessCodex)
	}
	cfg.Worker.Harness = domain.HarnessClaudeCode
	worker := seedRecord(ports.SpawnConfig{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex}, cfg, now)
	if worker.Metadata.RoleAgent != domain.HarnessClaudeCode {
		t.Fatalf("worker role agent = %q, want %q", worker.Metadata.RoleAgent, domain.HarnessClaudeCode)
	}
}

// Switching an orchestrator to another agent records the project's current
// orchestrator agent, so the switch never reads as the setting drifting.
func TestRecordAgentChoiceRecordsTheProjectsCurrentRoleAgent(t *testing.T) {
	base := newFakeStore()
	base.projects["p"] = domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{
		Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex},
	}}
	base.sessions["p-orchestrator"] = domain.SessionRecord{
		ID: "p-orchestrator", ProjectID: "p", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{RoleAgent: domain.HarnessGrok},
	}
	base.sessions["p-1"] = domain.SessionRecord{ID: "p-1", ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex}
	store := &roleAgentRecordingStore{fakeStore: base, roleAgents: map[domain.SessionID]domain.AgentHarness{}}
	m := &Manager{store: store}

	if err := m.RecordAgentChoice(context.Background(), "p-orchestrator"); err != nil {
		t.Fatalf("RecordAgentChoice(orchestrator): %v", err)
	}
	if err := m.RecordAgentChoice(context.Background(), "p-1"); err != nil {
		t.Fatalf("RecordAgentChoice(worker): %v", err)
	}
	if got := store.roleAgents["p-orchestrator"]; got != domain.HarnessCodex {
		t.Fatalf("orchestrator role agent = %q, want the project's orchestrator agent %q", got, domain.HarnessCodex)
	}
	if got := store.roleAgents["p-1"]; got != domain.HarnessClaudeCode {
		t.Fatalf("worker role agent = %q, want the project's worker agent %q", got, domain.HarnessClaudeCode)
	}
	if err := m.RecordAgentChoice(context.Background(), "missing"); err == nil {
		t.Fatal("RecordAgentChoice(missing) = nil, want not found")
	}
}
