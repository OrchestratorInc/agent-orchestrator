package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSpawnSpeedRequiresFreshAdvertisedCapabilities(t *testing.T) {
	for _, speed := range []domain.SpeedMode{domain.SpeedModeFast, domain.SpeedModeUltrafast} {
		for _, scenario := range []string{"supported", "missing", "stale", "other-harness"} {
			t.Run(string(speed)+"/"+scenario, func(t *testing.T) {
				tiers := []ports.ModelServiceTier{{ID: "priority"}, {ID: "ultrafast"}}
				if scenario == "missing" {
					tiers = nil
				}
				m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Stale: scenario == "stale", Models: []ports.AgentModelInfo{{ID: "m", IsDefault: true, ServiceTiers: tiers}}}}}
				harness := domain.HarnessCodex
				if scenario == "other-harness" {
					harness = domain.HarnessClaudeCode
				}
				resolved, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{Harness: harness, AgentConfig: ports.AgentConfig{Model: "m", SpeedMode: speed}}, domain.ProjectConfig{})
				if scenario == "supported" {
					if err != nil || resolved.SpeedMode != speed {
						t.Fatalf("resolved = %+v, error = %v", resolved, err)
					}
				} else if !errors.Is(err, ports.ErrUnsupportedSpeed) {
					t.Fatalf("error = %v", err)
				}
			})
		}
	}
}

func TestChatSpawnPersistsSpeedBeforeFirstTurn(t *testing.T) {
	for _, speed := range []domain.SpeedMode{domain.SpeedModeNormal, domain.SpeedModeFast, domain.SpeedModeUltrafast} {
		t.Run(string(speed), func(t *testing.T) {
			launcher := &recordingLauncher{}
			m, st, _ := newChatManager(launcher)
			m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "m", IsDefault: true, ServiceTiers: []ports.ModelServiceTier{{ID: "priority"}, {ID: "ultrafast"}}}}}}
			rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				RequestedMode: domain.SessionModeChat, AgentConfig: ports.AgentConfig{Model: "m", SpeedMode: speed},
			})
			if err != nil {
				t.Fatal(err)
			}
			if rec.Metadata.SpeedMode != speed || st.sessions[rec.ID].Metadata.SpeedMode != speed || launcher.started[0].SpeedMode != speed {
				t.Fatalf("speed was not propagated before first turn: record=%q, stored=%q, start=%q", rec.Metadata.SpeedMode, st.sessions[rec.ID].Metadata.SpeedMode, launcher.started[0].SpeedMode)
			}
		})
	}
}

func TestRoleSpeedOnlyAppliesToMatchingHarness(t *testing.T) {
	cfg := testRoleAgents()
	role := cfg.Worker
	role.Harness = domain.HarnessCodex
	role.AgentConfig.SpeedMode = domain.SpeedModeFast
	cfg.Worker = role
	if got := effectiveAgentConfig(domain.HarnessCodex, domain.KindWorker, cfg).SpeedMode; got != domain.SpeedModeFast {
		t.Fatalf("matching role speed = %q", got)
	}
	if got := effectiveAgentConfig(domain.HarnessClaudeCode, domain.KindWorker, cfg).SpeedMode; got != "" {
		t.Fatalf("Codex role speed leaked to another harness: %q", got)
	}
}

func TestAcceleratedRestoreKeepsSelectedModelAndEffort(t *testing.T) {
	config := domain.ProjectConfig{AgentConfig: domain.AgentConfig{Model: "project-default", Effort: "high"}}
	rec := domain.SessionRecord{Harness: domain.HarnessCodex, Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{Model: "chosen-model", Effort: "low", SpeedMode: domain.SpeedModeFast}}
	got := restoredAgentConfig(rec, config)
	if got.Model != "chosen-model" || got.Effort != "low" || got.SpeedMode != domain.SpeedModeFast {
		t.Fatalf("restored different model/speed pair: %+v", got)
	}
}

type normalRecoveryStore struct {
	*fakeStore
	recoveryCalls int
}

func (s *normalRecoveryStore) ResumeWithNormalSettings(_ context.Context, id domain.SessionID, revision int64) (bool, error) {
	rec := s.sessions[id]
	if rec.Revision != revision {
		return false, nil
	}
	s.recoveryCalls++
	rec.Metadata.SpeedMode = domain.SpeedModeNormal
	s.sessions[id] = rec
	return true, nil
}
func TestExplicitNormalResumePreservesNativeIdentityAfterCapabilityFailure(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, st, ws := newExitedResumeManager(t, runtime, agent)
	rec := st.sessions["mer-1"]
	rec.Metadata.Model = "chosen-model"
	rec.Metadata.SpeedMode = domain.SpeedModeFast
	st.sessions[rec.ID] = rec
	recovery := &normalRecoveryStore{fakeStore: st}
	m.store = recovery
	m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Stale: true}}
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ports.ErrUnsupportedSpeed) {
		t.Fatalf("ordinary retry error = %v", err)
	}
	if recovery.recoveryCalls != 0 || st.sessions[rec.ID].Metadata.SpeedMode != domain.SpeedModeFast {
		t.Fatal("ordinary retry discarded preference")
	}
	result, err := m.ResumeAgentWithNormal(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != RestoreModeNative || recovery.recoveryCalls != 1 {
		t.Fatalf("normal recovery = %+v, calls=%d", result, recovery.recoveryCalls)
	}
	if result.Session.Metadata.AgentSessionID != rec.Metadata.AgentSessionID || result.Session.Metadata.WorkspacePath != rec.Metadata.WorkspacePath || len(ws.calls) != 0 {
		t.Fatalf("normal recovery replaced native conversation/workspace: %+v", result)
	}
}
func TestNormalResumeRejectsLiveAgentBeforeChangingPreference(t *testing.T) {
	runtime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	m, st, _ := newExitedResumeManager(t, runtime, &fakeAgent{})
	rec := st.sessions["mer-1"]
	rec.Activity.State = domain.ActivityActive
	rec.Metadata.SpeedMode = domain.SpeedModeFast
	st.sessions[rec.ID] = rec
	recovery := &normalRecoveryStore{fakeStore: st}
	m.store = recovery
	if _, err := m.ResumeAgentWithNormal(context.Background(), rec.ID); !errors.Is(err, ErrAgentNotExited) {
		t.Fatalf("error=%v", err)
	}
	if recovery.recoveryCalls != 0 {
		t.Fatal("changed a live agent preference")
	}
}

type accountSpeedCatalog struct {
	tuningCatalog
	sessionID domain.SessionID
	accountID string
}

func (c *accountSpeedCatalog) ModelsForSession(_ context.Context, _, _ string, sessionID domain.SessionID, accountID string, _ bool) (ports.AgentModelCatalog, error) {
	c.sessionID, c.accountID = sessionID, accountID
	return c.catalog, c.err
}
func TestSpeedValidationUsesSessionAndTargetAccount(t *testing.T) {
	catalog := &accountSpeedCatalog{tuningCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "m", ServiceTiers: []ports.ModelServiceTier{{ID: "priority"}}}}}}}
	m := &Manager{modelCatalog: catalog}
	cfg := ports.SpawnConfig{Harness: domain.HarnessCodex, AgentConfig: ports.AgentConfig{Model: "m", SpeedMode: domain.SpeedModeFast}}
	if _, err := m.resolveAgentConfigForSession(context.Background(), cfg, domain.ProjectConfig{}, "session-1", "target-account"); err != nil {
		t.Fatal(err)
	}
	if catalog.sessionID != "session-1" || catalog.accountID != "target-account" {
		t.Fatalf("lookup scope=%s/%s", catalog.sessionID, catalog.accountID)
	}
}

func TestTUIAcceleratedSpawnNeverBypassesCatalogValidation(t *testing.T) {
	for _, scenario := range []string{"missing", "stale", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, st, _ := newExitedResumeManager(t, &fakeRuntime{}, &fakeAgent{})
			catalog := tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "m", IsDefault: true}}}}
			if scenario == "stale" {
				catalog.catalog.Stale = true
			}
			if scenario == "unavailable" {
				catalog.err = errors.New("catalog offline")
			}
			m.modelCatalog = catalog
			before := len(st.sessions)
			_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "m", SpeedMode: domain.SpeedModeFast}})
			if !errors.Is(err, ports.ErrUnsupportedSpeed) && !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
				t.Fatalf("spawn error=%v", err)
			}
			if len(st.sessions) != before {
				t.Fatal("rejected accelerated spawn created durable session")
			}
		})
	}
}
