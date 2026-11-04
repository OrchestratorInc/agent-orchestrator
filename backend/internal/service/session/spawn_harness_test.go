package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// agentReadinessByID reports a snapshot per agent; an unlisted agent is unknown.
type agentReadinessByID map[string]domain.AgentReadinessSnapshot

func (r agentReadinessByID) EnsureAgentReadiness(_ context.Context, id string, _ domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	if s, ok := r[id]; ok {
		return s, nil
	}
	return domain.AgentReadinessSnapshot{ID: id}, nil
}
func (agentReadinessByID) InvalidateAgentInstallation(string)   {}
func (agentReadinessByID) InvalidateAgentAuthentication(string) {}
func (agentReadinessByID) RecheckAgent(string)                  {}

func readySnapshot(id string) domain.AgentReadinessSnapshot {
	return domain.AgentReadinessSnapshot{
		ID:             id,
		Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
		Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized},
	}
}

func notInstalledSnapshot(id string) domain.AgentReadinessSnapshot {
	return domain.AgentReadinessSnapshot{
		ID:           id,
		Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled},
	}
}

func TestSpawnWithoutHarnessPicksDefaultPriorityAmongReadyAgents(t *testing.T) {
	tests := []struct {
		name      string
		readiness agentReadinessByID
		want      domain.AgentHarness
	}{
		{"claude when it is ready", agentReadinessByID{"claude-code": readySnapshot("claude-code"), "codex": readySnapshot("codex")}, domain.HarnessClaudeCode},
		{"codex when claude is not installed", agentReadinessByID{"claude-code": notInstalledSnapshot("claude-code"), "codex": readySnapshot("codex")}, domain.HarnessCodex},
		{"cursor when only it is ready", agentReadinessByID{"claude-code": notInstalledSnapshot("claude-code"), "codex": notInstalledSnapshot("codex"), "cursor": readySnapshot("cursor")}, domain.HarnessCursor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
				Worker: domain.RoleOverride{Harness: domain.HarnessAider},
			}}
			fc := &fakeCommander{}
			svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: tt.readiness})

			if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if fc.spawnedCfg.Harness != tt.want {
				t.Fatalf("spawned on %q, want %q (a project's old pin must not decide)", fc.spawnedCfg.Harness, tt.want)
			}
		})
	}
}

func TestSpawnWorkerFollowsItsParentsCurrentHarness(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, AgentReadiness: agentReadinessByID{"claude-code": readySnapshot("claude-code")}})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "mer-orch"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if fc.spawnedCfg.Harness != domain.HarnessCodex {
		t.Fatalf("worker spawned on %q, want its switched parent's codex", fc.spawnedCfg.Harness)
	}
}

func TestSpawnExplicitHarnessBeatsParentAndDefault(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st})

	if _, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: "mer-orch", Harness: domain.HarnessGemini}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if fc.spawnedCfg.Harness != domain.HarnessGemini {
		t.Fatalf("spawned on %q, want the explicit gemini", fc.spawnedCfg.Harness)
	}
}

func TestSpawnOrchestratorCleanKeepsTheRetiredOrchestratorsHarness(t *testing.T) {
	st := newFakeStore()
	// The project's old pin says Claude; the live orchestrator was switched to Codex.
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if fc.spawnedCfg.Harness != domain.HarnessCodex {
		t.Fatalf("replacement spawned on %q, want the retired orchestrator's codex", fc.spawnedCfg.Harness)
	}
}
