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
