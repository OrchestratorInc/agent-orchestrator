package session

import (
	"context"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// roleFillingStore adds the optional role-agent filler to fakeStore.
type roleFillingStore struct {
	*fakeStore
	fills []domain.AgentHarness
}

func (f *roleFillingStore) FillProjectRoleAgents(_ context.Context, id string, agent domain.AgentHarness) (domain.ProjectRecord, bool, error) {
	f.fills = append(f.fills, agent)
	row, ok := f.projects[id]
	if !ok {
		return domain.ProjectRecord{}, false, nil
	}
	changed := row.Config.FillRoleAgents(agent)
	f.projects[id] = row
	return row, changed, nil
}

func pickAgent(agent domain.AgentHarness) func(context.Context) domain.AgentHarness {
	return func(context.Context) domain.AgentHarness { return agent }
}

func TestSpawnOrchestratorFillsMissingProjectRoleAgents(t *testing.T) {
	st := &roleFillingStore{fakeStore: newFakeStore()}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessCodex}}}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	svc.SetPreferredProjectAgent(pickAgent(domain.HarnessClaudeCode))

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	// The picked agent goes through the launch checks like an explicit one.
	if fc.spawnedCfg.Harness != domain.HarnessClaudeCode {
		t.Fatalf("spawn harness = %q, want claude-code", fc.spawnedCfg.Harness)
	}
	got := st.projects["mer"].Config
	// The chosen worker is kept; only the empty orchestrator role is filled.
	if got.Worker.Harness != domain.HarnessCodex || got.Orchestrator.Harness != domain.HarnessClaudeCode {
		t.Fatalf("roles = worker %q orchestrator %q", got.Worker.Harness, got.Orchestrator.Harness)
	}
}

func TestOrchestratorStartFillsMissingWorkerWithoutChangingOrchestrator(t *testing.T) {
	st := &roleFillingStore{fakeStore: newFakeStore()}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex},
	}}
	fc := &fakeCommander{spawnRecord: domain.SessionRecord{
		ID: "mer-9", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
	}}
	svc := &Service{manager: fc, store: st}
	svc.SetPreferredProjectAgent(pickAgent(domain.HarnessClaudeCode))

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	got := st.projects["mer"].Config
	if got.Worker.Harness != domain.HarnessClaudeCode || got.Orchestrator.Harness != domain.HarnessCodex {
		t.Fatalf("roles = worker %q orchestrator %q", got.Worker.Harness, got.Orchestrator.Harness)
	}
	if fc.spawnedCfg.Harness != "" {
		t.Fatalf("spawn overrode configured orchestrator with %q", fc.spawnedCfg.Harness)
	}
}

func TestFillMissingRoleAgentsLeavesProjectAlone(t *testing.T) {
	named := domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{
		Worker:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		Orchestrator: domain.RoleOverride{Harness: domain.HarnessCodex},
	}}
	empty := domain.ProjectRecord{ID: "mer"}
	cases := []struct {
		name    string
		project domain.ProjectRecord
		pick    func(context.Context) domain.AgentHarness
	}{
		{"both roles name an agent", named, pickAgent(domain.HarnessClaudeCode)},
		{"no preferred agent source", empty, nil},
		{"no agent could be ranked", empty, pickAgent("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &roleFillingStore{fakeStore: newFakeStore()}
			st.projects["mer"] = tc.project
			svc := &Service{store: st}
			svc.SetPreferredProjectAgent(tc.pick)
			got := svc.fillMissingRoleAgents(context.Background(), tc.project)
			if len(st.fills) != 0 {
				t.Fatalf("fills = %v, want none", st.fills)
			}
			if got.Config.Worker.Harness != tc.project.Config.Worker.Harness || got.Config.Orchestrator.Harness != tc.project.Config.Orchestrator.Harness {
				t.Fatalf("roles changed: worker %q orchestrator %q", got.Config.Worker.Harness, got.Config.Orchestrator.Harness)
			}
		})
	}

	// A store that cannot fill leaves the spawn to report the missing agent.
	svc := &Service{store: newFakeStore()}
	svc.SetPreferredProjectAgent(pickAgent(domain.HarnessClaudeCode))
	if got := svc.fillMissingRoleAgents(context.Background(), empty); got.Config.Worker.Harness != "" {
		t.Fatalf("worker = %q, want empty", got.Config.Worker.Harness)
	}
}

type perAgentReadiness struct {
	snapshots map[string]domain.AgentReadinessSnapshot
	calls     []string
	purposes  []domain.AgentReadinessPurpose
	onCheck   func(agentID string)
}

func (f *perAgentReadiness) EnsureAgentReadiness(_ context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	f.calls = append(f.calls, agentID)
	f.purposes = append(f.purposes, purpose)
	if f.onCheck != nil {
		f.onCheck(agentID)
	}
	return f.snapshots[agentID], nil
}

func (f *perAgentReadiness) InvalidateAgentInstallation(string)   {}
func (f *perAgentReadiness) InvalidateAgentAuthentication(string) {}
func (f *perAgentReadiness) RecheckAgent(string)                  {}

func TestFillMissingRoleAgentsSkipsStaleUnlaunchableCandidateWithoutPersisting(t *testing.T) {
	cases := []struct {
		name      string
		candidate domain.AgentHarness
		snapshot  domain.AgentReadinessSnapshot
	}{
		{
			name:      "removed binary",
			candidate: domain.HarnessClaudeCode,
			snapshot: domain.AgentReadinessSnapshot{
				ID:           "claude-code",
				Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled},
			},
		},
		{
			name:      "signed-out codex",
			candidate: domain.HarnessCodex,
			snapshot: domain.AgentReadinessSnapshot{
				ID:           "codex",
				Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
				Authentication: domain.AgentAuthenticationObservation{
					State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &roleFillingStore{fakeStore: newFakeStore()}
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			readiness := &perAgentReadiness{
				snapshots: map[string]domain.AgentReadinessSnapshot{string(tc.candidate): tc.snapshot},
			}
			svc := &Service{store: st, agentReadiness: readiness}
			svc.SetPreferredProjectAgent(pickAgent(tc.candidate))

			got := svc.fillMissingRoleAgents(context.Background(), st.projects["mer"])
			if len(st.fills) != 0 {
				t.Fatalf("fills = %v, want none when launch validation rejects candidate", st.fills)
			}
			if got.Config.Worker.Harness != "" || got.Config.Orchestrator.Harness != "" {
				t.Fatalf("roles persisted on failure: worker %q orchestrator %q", got.Config.Worker.Harness, got.Config.Orchestrator.Harness)
			}
			if len(readiness.purposes) != 1 || readiness.purposes[0] != domain.AgentReadinessPurposeLaunch {
				t.Fatalf("readiness purposes = %v, want one launch check", readiness.purposes)
			}
		})
	}
}

func TestFillMissingRoleAgentsFallsBackAfterStaleCandidateFailsLaunchCheck(t *testing.T) {
	st := &roleFillingStore{fakeStore: newFakeStore()}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	fc := &fakeCommander{}
	// Simulate stale cached readiness: claude-code was cached as installed and
	// authorized, but launch validation discovers it is gone and updates the cache.
	cached := []domain.AgentReadinessSnapshot{
		{
			ID:             "claude-code",
			Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
			Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized},
		},
		{
			ID:             "codex",
			Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
			Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized},
		},
	}
	readiness := &perAgentReadiness{
		snapshots: map[string]domain.AgentReadinessSnapshot{
			"claude-code": {
				ID:           "claude-code",
				Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationNotInstalled},
			},
			"codex": {
				ID:             "codex",
				Installation:   domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
				Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized, Freshness: domain.AgentReadinessFresh},
			},
		},
		onCheck: func(agentID string) {
			if agentID == "claude-code" {
				cached[0].Installation.State = domain.AgentInstallationNotInstalled
			}
		},
	}
	svc := &Service{manager: fc, store: st, agentReadiness: readiness}
	svc.SetPreferredProjectAgent(func(context.Context) domain.AgentHarness {
		return domain.PreferredInstalledProjectAgent(cached)
	})

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false, "", ""); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(st.fills) != 1 || st.fills[0] != domain.HarnessCodex {
		t.Fatalf("fills = %v, want only [codex]", st.fills)
	}
	if fc.spawnedCfg.Harness != domain.HarnessCodex {
		t.Fatalf("spawn harness = %q, want codex", fc.spawnedCfg.Harness)
	}
	got := st.projects["mer"].Config
	if got.Worker.Harness != domain.HarnessCodex || got.Orchestrator.Harness != domain.HarnessCodex {
		t.Fatalf("roles = worker %q orchestrator %q, want codex for both", got.Worker.Harness, got.Orchestrator.Harness)
	}
}

func TestFillMissingRoleAgentsAllowsSavedAccountWhenComputerLoginUnauthorized(t *testing.T) {
	st := &roleFillingStore{fakeStore: newFakeStore()}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	readiness := &perAgentReadiness{
		snapshots: map[string]domain.AgentReadinessSnapshot{
			"codex": {
				ID:           "codex",
				Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
				Authentication: domain.AgentAuthenticationObservation{
					State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
				},
			},
		},
	}
	svc := &Service{store: st, agentReadiness: readiness}
	svc.SetPreferredProjectAgent(pickAgent(domain.HarnessCodex))
	svc.SetComputerLoginCheck(func(_ context.Context, _ domain.AgentHarness, _ string) bool { return false })

	got := svc.fillMissingRoleAgents(context.Background(), st.projects["mer"])
	if got.Config.Worker.Harness != domain.HarnessCodex || got.Config.Orchestrator.Harness != domain.HarnessCodex {
		t.Fatalf("roles = worker %q orchestrator %q, want codex when using saved account", got.Config.Worker.Harness, got.Config.Orchestrator.Harness)
	}
}
