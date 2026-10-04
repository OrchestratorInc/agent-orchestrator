package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestResolveLaunchEffortUsesControllerAndAdvertisedChoices(t *testing.T) {
	for _, harness := range domain.AllHarnesses {
		for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
			t.Run(string(harness)+"/"+string(mode), func(t *testing.T) {
				calls := 0
				m := &Manager{modelCatalog: tuningCatalog{calls: &calls, catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
					{ID: "model", Efforts: []string{"high"}},
				}}}}
				resolved, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
					Harness: harness, RequestedMode: mode, AgentConfig: ports.AgentConfig{Model: "model", Effort: "high"}, EffortOverride: true,
				}, domain.ProjectConfig{})
				if err != nil {
					t.Fatal(err)
				}
				wantSupported := harness == domain.HarnessCodex || harness == domain.HarnessClaudeCode ||
					(mode == domain.SessionModeChat && (harness == domain.HarnessPi || harness == domain.HarnessOpenCode ||
						harness == domain.HarnessOpenCodeV2 || harness == domain.HarnessDeepSeek || harness == domain.HarnessUnreal))
				if wantSupported {
					if resolved.Effort != "high" || calls != 1 {
						t.Fatalf("supported effort = %q, catalog calls = %d", resolved.Effort, calls)
					}
				} else if resolved.Effort != "" || calls != 0 {
					t.Fatalf("ignored effort remains = %q, catalog calls = %d", resolved.Effort, calls)
				}
			})
		}
	}
}

func TestResolveNativeChatEffortRejectsUnverifiedOrUnsupportedChoices(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessPi, domain.HarnessOpenCode, domain.HarnessOpenCodeV2, domain.HarnessDeepSeek, domain.HarnessUnreal} {
		for _, scenario := range []struct {
			name    string
			catalog ports.AgentModelCatalog
			missing bool
			want    error
		}{
			{name: "unsupported", catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "model", Efforts: []string{"low"}}}}, want: ports.ErrUnsupportedEffort},
			{name: "not advertised", catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "model"}}}, want: ports.ErrUnsupportedEffort},
			{name: "stale", catalog: ports.AgentModelCatalog{Stale: true, Models: []ports.AgentModelInfo{{ID: "model", Efforts: []string{"high"}}}}, want: ports.ErrModelCapabilitiesUnavailable},
			{name: "missing catalog", missing: true, want: ports.ErrModelCapabilitiesUnavailable},
		} {
			t.Run(string(harness)+"/"+scenario.name, func(t *testing.T) {
				m := &Manager{}
				if !scenario.missing {
					m.modelCatalog = tuningCatalog{catalog: scenario.catalog}
				}
				_, err := m.resolveAgentConfig(context.Background(), ports.SpawnConfig{
					Harness: harness, RequestedMode: domain.SessionModeChat, AgentConfig: ports.AgentConfig{Model: "model", Effort: "high"}, EffortOverride: true,
				}, domain.ProjectConfig{})
				if !errors.Is(err, scenario.want) {
					t.Fatalf("error = %v, want %v", err, scenario.want)
				}
			})
		}
	}
}

func TestChatSpawnPreservesNativeAdvertisedEffort(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessPi, domain.HarnessOpenCode, domain.HarnessOpenCodeV2, domain.HarnessDeepSeek, domain.HarnessUnreal} {
		for _, requested := range []domain.SessionMode{domain.SessionModeChat, ""} {
			t.Run(string(harness)+"/"+string(requested), func(t *testing.T) {
				launcher := &recordingLauncher{}
				m, _, _ := newChatManager(launcher)
				m.dataDir = t.TempDir()
				m.defaults = fixedSessionModeDefaults(domain.SessionModeChat)
				m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "model", Efforts: []string{"high"}}}}}
				rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: requested,
					AgentConfig: ports.AgentConfig{Model: "model", Effort: "high"}, EffortOverride: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if rec.Metadata.Effort != "high" || len(launcher.started) != 1 || launcher.started[0].Effort != "high" {
					t.Fatalf("launch did not preserve effort: metadata %q, starts %#v", rec.Metadata.Effort, launcher.started)
				}
			})
		}
	}
}

func TestTUISpawnClearsEffortUnsupportedByController(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessPi, domain.HarnessOpenCode, domain.HarnessOpenCodeV2, domain.HarnessDeepSeek, domain.HarnessUnreal} {
		for _, fallback := range []bool{false, true} {
			if harness == domain.HarnessUnreal && fallback {
				continue
			}
			t.Run(string(harness)+"/"+map[bool]string{false: "explicit", true: "fallback"}[fallback], func(t *testing.T) {
				launcher := &recordingLauncher{}
				m, _, _ := newChatManager(launcher)
				m.dataDir = t.TempDir()
				requested := domain.SessionModeTUI
				if fallback {
					requested = ""
					m.defaults = fixedSessionModeDefaults(domain.SessionModeChat)
					launcher.preflightErr = ports.ErrChatUnsupported
				}
				rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: requested,
					AgentConfig: ports.AgentConfig{Effort: "high"}, EffortOverride: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if rec.Metadata.Effort != "" || len(launcher.started) != 0 {
					t.Fatalf("unsupported TUI effort persisted %q, chat starts %d", rec.Metadata.Effort, len(launcher.started))
				}
			})
		}
	}
}

func TestTUISpawnExplicitEffortDefaultClearsInheritedChoice(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, override := range []bool{false, true} {
			t.Run(string(harness)+"/"+map[bool]string{false: "inherit", true: "provider default"}[override], func(t *testing.T) {
				m, st, _, _ := newManager()
				m.dataDir = t.TempDir()
				agent := &recordingAgent{}
				m.agents = singleAgent{agent: agent}
				project := st.projects[string(chatTestProject)]
				project.Config.Worker.Harness = harness
				project.Config.Worker.AgentConfig = domain.AgentConfig{Model: "model", Effort: "high"}
				st.projects[string(chatTestProject)] = project
				m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "model", Efforts: []string{"high"}}}}}
				rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeTUI, EffortOverride: override,
				})
				if err != nil {
					t.Fatal(err)
				}
				want := "high"
				if override {
					want = ""
				}
				if rec.Metadata.Effort != want || agent.lastLaunch.Config.Effort != want {
					t.Fatalf("TUI effort metadata/launch = %q/%q, want %q", rec.Metadata.Effort, agent.lastLaunch.Config.Effort, want)
				}
				rec.IsTerminated = true
				rec.Activity.State = domain.ActivityExited
				rec.Metadata.AgentSessionID = "native-effort-reset"
				st.sessions[rec.ID] = rec
				project.Config.Worker.AgentConfig.Effort = "changed-project-effort"
				st.projects[string(chatTestProject)] = project
				if _, err := m.RestoreWithMode(context.Background(), rec.ID); err != nil {
					t.Fatal(err)
				}
				if agent.lastRestore.Config.Effort != want || agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != "native-effort-reset" {
					t.Fatalf("native restore lost effort or conversation: %#v", agent.lastRestore)
				}
			})
		}
	}
}

func TestPickedChatEffortSurvivesNativeTUIRestore(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, effort := range []string{"high", ""} {
			t.Run(string(harness)+"/"+effort, func(t *testing.T) {
				ctx := context.Background()
				m, st, _, _ := newManager()
				m.dataDir = t.TempDir()
				agent := &recordingAgent{}
				m.agents = singleAgent{agent: agent}
				project := st.projects[string(chatTestProject)]
				project.Config.Worker.Harness = harness
				project.Config.Worker.AgentConfig = domain.AgentConfig{Model: "model", Effort: "medium"}
				st.projects[string(chatTestProject)] = project
				m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "model", Efforts: []string{"medium", "high"}}}}}
				rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: harness, RequestedMode: domain.SessionModeTUI})
				if err != nil {
					t.Fatal(err)
				}
				if err := m.PersistChatEffort(ctx, rec.ID, effort); err != nil {
					t.Fatal(err)
				}
				rec = st.sessions[rec.ID]
				rec.IsTerminated = true
				rec.Activity.State = domain.ActivityExited
				rec.Metadata.AgentSessionID = "native-live-effort"
				st.sessions[rec.ID] = rec
				if _, err := m.RestoreWithMode(ctx, rec.ID); err != nil {
					t.Fatal(err)
				}
				if agent.lastRestore.Config.Effort != effort || agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != "native-live-effort" {
					t.Fatalf("restore = %#v, want effort %q and same native conversation", agent.lastRestore, effort)
				}
			})
		}
	}
}
