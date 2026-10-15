package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/litmus"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

func newTestingManager(t *testing.T, harness domain.AgentHarness) (*Manager, *fakeStore, *recordingAgent, *fakeRuntime, *fakeWorkspace, string) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{Worker: domain.RoleOverride{Harness: harness}}}
	agent, rt, ws, dataDir := &recordingAgent{}, &fakeRuntime{}, &fakeWorkspace{}, t.TempDir()
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st}, DataDir: dataDir, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	return m, st, agent, rt, ws, dataDir
}

func testingSpawn(t *testing.T, m *Manager) domain.SessionRecord {
	t.Helper()
	rec, _, _, err := m.Spawn(WithTestingProfile(ctx), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI, Prompt: "Test PR"})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func wantPolicy(t *testing.T, perm ports.PermissionMode, allowed, denied []string) {
	t.Helper()
	wantAllowed, wantDenied := litmus.ToolPolicy()
	if perm != ports.PermissionModeDontAsk || !slices.Equal(allowed, wantAllowed) || !slices.Equal(denied, wantDenied) {
		t.Fatalf("policy = %q allow %v deny %v, want dontAsk with the testing lists", perm, allowed, denied)
	}
}

func TestSpawnTestingProfileLaunchesInRunDirUnderPolicy(t *testing.T) {
	m, st, agent, rt, ws, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
	rec := testingSpawn(t, m)
	runDir := litmus.RunDir(dataDir, string(rec.ID))

	wantPolicy(t, agent.lastLaunch.Permissions, agent.lastLaunch.AllowedTools, agent.lastLaunch.DisallowedTools)
	if agent.lastLaunch.WorkspacePath != runDir || rt.lastCfg.WorkspacePath != runDir || st.sessions[rec.ID].Metadata.WorkspacePath != runDir {
		t.Fatalf("cwd launch %q runtime %q, want the run dir %q", agent.lastLaunch.WorkspacePath, rt.lastCfg.WorkspacePath, runDir)
	}
	if got := rt.lastCfg.Env[litmus.EnvRun]; got != "run_"+string(rec.ID) {
		t.Fatalf("%s = %q", litmus.EnvRun, got)
	}
	if c := agent.lastLaunch.Config; c.Model != litmus.TesterModel || c.Effort != litmus.TesterEffort {
		t.Fatalf("tester runs on %q at %q, want %q at %q", c.Model, c.Effort, litmus.TesterModel, litmus.TesterEffort)
	}
	if rt.lastCfg.Env["GIT_CONFIG_COUNT"] == "" || rt.lastCfg.Env["GIT_CONFIG_KEY_0"] != "url.litmus-blocks-push://.pushInsteadOf" {
		t.Fatalf("tester env has no push block: %v", rt.lastCfg.Env)
	}
	if gh, ok := rt.lastCfg.Env["GH_TOKEN"]; !ok || gh != "" || rt.lastCfg.Env["GH_CONFIG_DIR"] != filepath.Join(runDir, "gh-config") {
		t.Fatalf("tester env keeps a gh login: token %q config %q", gh, rt.lastCfg.Env["GH_CONFIG_DIR"])
	}
	if info, err := os.Stat(runDir); err != nil || !info.IsDir() {
		t.Fatalf("run dir not created: %v", err)
	}
	if ws.createCount != 0 || st.sessions[rec.ID].Metadata.Branch != "" {
		t.Fatalf("a worktree was made: creates=%d branch=%q", ws.createCount, st.sessions[rec.ID].Metadata.Branch)
	}

	// An ordinary spawn is untouched.
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI}); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.Permissions == ports.PermissionModeDontAsk || len(agent.lastLaunch.AllowedTools)+len(agent.lastLaunch.DisallowedTools) != 0 || rt.lastCfg.Env[litmus.EnvRun] != "" {
		t.Fatalf("ordinary spawn got the testing profile: %+v", agent.lastLaunch)
	}
	if _, has := rt.lastCfg.Env["GH_TOKEN"]; has || rt.lastCfg.Env["GH_CONFIG_DIR"] != "" {
		t.Fatalf("ordinary spawn lost its gh login: %v", rt.lastCfg.Env)
	}
	if c := agent.lastLaunch.Config; c.Model != "" || c.Effort != "" || rt.lastCfg.Env["GIT_CONFIG_COUNT"] != "" {
		t.Fatalf("ordinary spawn got the tester's model or the push block: %q %q %v", c.Model, c.Effort, rt.lastCfg.Env)
	}
}

func TestSpawnTestingProfileModelOrder(t *testing.T) {
	catalog := tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "sonnet", IsDefault: true, Efforts: []string{"low", "high"}},
		{ID: "opus", Efforts: []string{"high", "max"}},
		{ID: "haiku", Efforts: []string{"low"}},
	}}}
	for _, tc := range []struct {
		name                       string
		worker                     domain.RoleOverride
		settingsFile               bool // a settings file exists, whatever it holds
		settingModel, settingEffrt string
		wantModel, wantEffort      string
	}{
		{name: "default", wantModel: litmus.TesterModel, wantEffort: litmus.TesterEffort},
		{name: "project config beats default", worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "haiku", Effort: "low"}},
			wantModel: "haiku", wantEffort: "low"},
		{name: "setting beats project config", worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "haiku", Effort: "low"}},
			settingModel: "opus", settingEffrt: "max", wantModel: "opus", wantEffort: "max"},
		{name: "setting beats default", settingModel: "opus", settingEffrt: "max", wantModel: "opus", wantEffort: "max"},
		{name: "empty setting falls through", settingsFile: true, worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: "haiku", Effort: "low"}},
			wantModel: "haiku", wantEffort: "low"},
		{name: "setting model alone keeps the default effort", settingModel: "opus", wantModel: "opus", wantEffort: litmus.TesterEffort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, agent, _, _, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
			m.SetModelCatalog(catalog)
			p := st.projects["mer"]
			if tc.worker.Harness != "" {
				p.Config.Worker = tc.worker
				st.projects["mer"] = p
			}
			if tc.settingsFile || tc.settingModel != "" || tc.settingEffrt != "" {
				svc := litmus.New(litmus.NewStore(dataDir), time.Now)
				if _, err := svc.SetProjectSettings(ctx, "mer", litmus.ProjectSettings{Model: tc.settingModel, Effort: tc.settingEffrt}); err != nil {
					t.Fatal(err)
				}
			}
			testingSpawn(t, m)
			if c := agent.lastLaunch.Config; c.Model != tc.wantModel || c.Effort != tc.wantEffort {
				t.Fatalf("tester runs on %q at %q, want %q at %q", c.Model, c.Effort, tc.wantModel, tc.wantEffort)
			}
		})
	}
}

func TestSpawnTestingProfileSettingNeverReachesOrdinarySpawns(t *testing.T) {
	m, _, agent, _, _, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
	svc := litmus.New(litmus.NewStore(dataDir), time.Now)
	if _, err := svc.SetProjectSettings(ctx, "mer", litmus.ProjectSettings{Model: "opus", Effort: "max"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI}); err != nil {
		t.Fatal(err)
	}
	if c := agent.lastLaunch.Config; c.Model != "" || c.Effort != "" {
		t.Fatalf("an ordinary spawn took the tester's setting: %q %q", c.Model, c.Effort)
	}
}

func TestSpawnTestingProfileRefusesOtherHarnesses(t *testing.T) {
	m, st, _, rt, ws, _ := newTestingManager(t, domain.HarnessAider)
	_, _, _, err := m.Spawn(WithTestingProfile(ctx), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RequestedMode: domain.SessionModeTUI})
	if !errors.Is(err, ErrTestingHarness) || len(st.sessions) != 0 || ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("err %v sessions %d workspaces %d runtimes %d", err, len(st.sessions), ws.createCount, rt.created)
	}
}

// A Codex tester has no tool lists: it launches unattended in its run dir with the same env, the model and effort from
// the project's Litmus settings, and the litmus skill in its system prompt.
func TestSpawnTestingProfileOnCodex(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		settings              litmus.ProjectSettings
		wantModel, wantEffort string
	}{
		{name: "settings", settings: litmus.ProjectSettings{Model: "gpt-6.1-sol", Effort: "xhigh"}, wantModel: "gpt-6.1-sol", wantEffort: "xhigh"},
		{name: "defaults keep codex's own model", wantEffort: litmus.TesterEffort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, agent, rt, ws, dataDir := newTestingManager(t, domain.HarnessCodex)
			if _, err := litmus.New(litmus.NewStore(dataDir), time.Now).SetProjectSettings(ctx, "mer", tc.settings); err != nil {
				t.Fatal(err)
			}
			rec := testingSpawn(t, m)
			runDir := litmus.RunDir(dataDir, string(rec.ID))

			l := agent.lastLaunch
			if l.Permissions != ports.PermissionModeBypassPermissions || len(l.AllowedTools)+len(l.DisallowedTools) != 0 {
				t.Fatalf("codex tester policy = %q allow %v deny %v, want bypass and no tool lists", l.Permissions, l.AllowedTools, l.DisallowedTools)
			}
			if l.Config.Model != tc.wantModel || l.Config.Effort != tc.wantEffort {
				t.Fatalf("tester runs on %q at %q, want %q at %q", l.Config.Model, l.Config.Effort, tc.wantModel, tc.wantEffort)
			}
			if l.WorkspacePath != runDir || rt.lastCfg.WorkspacePath != runDir || ws.createCount != 0 {
				t.Fatalf("cwd launch %q runtime %q creates %d, want the run dir %q", l.WorkspacePath, rt.lastCfg.WorkspacePath, ws.createCount, runDir)
			}
			if rt.lastCfg.Env[litmus.EnvRun] != "run_"+string(rec.ID) || rt.lastCfg.Env["GIT_CONFIG_KEY_0"] != "url.litmus-blocks-push://.pushInsteadOf" {
				t.Fatalf("tester env lacks the run id or the push block: %v", rt.lastCfg.Env)
			}
			skill := skillassets.LitmusDir(dataDir) + "/SKILL.md"
			if !strings.Contains(l.SystemPrompt, skill) || strings.Contains(l.SystemPrompt, skillassets.Dir(dataDir)) {
				t.Fatalf("codex system prompt does not point only at the litmus skill:\n%s", l.SystemPrompt)
			}
			if st.sessions[rec.ID].Harness != domain.HarnessCodex {
				t.Fatalf("session harness = %q", st.sessions[rec.ID].Harness)
			}
		})
	}
}

func TestRelaunchTestingSessionOnCodexKeepsItsPolicy(t *testing.T) {
	m, st, agent, _, _, dataDir := newTestingManager(t, domain.HarnessCodex)
	runDir := litmus.RunDir(dataDir, "mer-1")
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{WorkspacePath: runDir, Prompt: "Test PR", RuntimeHandleID: "h1"}}
	st.sessions[rec.ID] = rec
	info, err := m.restoreSessionWorkspace(ctx, st.projects["mer"], rec)
	if err != nil || info.Path != runDir {
		t.Fatalf("workspace %+v err %v", info, err)
	}
	if _, err := m.relaunchSession(ctx, "restore", rec, st.projects["mer"], info, nil); err != nil {
		t.Fatal(err)
	}
	l := agent.lastLaunch
	if l.Permissions != ports.PermissionModeBypassPermissions || len(l.AllowedTools)+len(l.DisallowedTools) != 0 || l.Config.Model != "" || l.Config.Effort != litmus.TesterEffort {
		t.Fatalf("relaunched codex tester: %q allow %v deny %v on %q at %q", l.Permissions, l.AllowedTools, l.DisallowedTools, l.Config.Model, l.Config.Effort)
	}
}

func TestRelaunchTestingSessionCarriesPolicyOnBothPaths(t *testing.T) {
	for name, native := range map[string]string{"native resume": "agent-x", "fresh launch": ""} {
		t.Run(name, func(t *testing.T) {
			m, st, agent, _, ws, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
			runDir := litmus.RunDir(dataDir, "mer-1")
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
				Metadata: domain.SessionMetadata{WorkspacePath: runDir, AgentSessionID: native, Prompt: "Test PR", RuntimeHandleID: "h1"}}
			wantModel, wantEffort := litmus.TesterModel, litmus.TesterEffort
			if native != "" { // a model and effort the user chose are kept; the fresh launch has none and gets the defaults
				rec.Metadata.Model, rec.Metadata.Effort = "opus", "max"
				wantModel, wantEffort = "opus", "max"
			}
			st.sessions[rec.ID] = rec
			info, err := m.restoreSessionWorkspace(ctx, st.projects["mer"], rec) // what reconcile does before relaunching
			if err != nil || info.Path != runDir {
				t.Fatalf("workspace %+v err %v", info, err)
			}
			if _, err := m.relaunchSession(ctx, "restore", rec, st.projects["mer"], info, nil); err != nil {
				t.Fatal(err)
			}
			cfg := agent.lastLaunch.Config
			if native != "" {
				wantPolicy(t, agent.lastRestore.Permissions, agent.lastRestore.AllowedTools, agent.lastRestore.DisallowedTools)
				cfg = agent.lastRestore.Config
			} else {
				wantPolicy(t, agent.lastLaunch.Permissions, agent.lastLaunch.AllowedTools, agent.lastLaunch.DisallowedTools)
			}
			if cfg.Model != wantModel || cfg.Effort != wantEffort {
				t.Fatalf("relaunched tester runs on %q at %q, want %q at %q", cfg.Model, cfg.Effort, wantModel, wantEffort)
			}
			if ws.createCount != 0 || len(ws.restoreConfigs) != 0 {
				t.Fatalf("restore went to the worktree adapter: %d creates %d restores", ws.createCount, len(ws.restoreConfigs))
			}
		})
	}
}

func TestKillTestingSessionKeepsRunDir(t *testing.T) {
	m, _, _, rt, ws, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
	rec := testingSpawn(t, m)
	if err := os.WriteFile(litmus.RunDir(dataDir, string(rec.ID))+"/verdict.md", []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Kill(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if rt.destroyed != 1 || ws.destroyed != 0 {
		t.Fatalf("runtime destroyed %d, workspace destroyed %d", rt.destroyed, ws.destroyed)
	}
	if _, err := os.Stat(litmus.RunDir(dataDir, string(rec.ID)) + "/verdict.md"); err != nil {
		t.Fatalf("kill removed the evidence: %v", err)
	}
}

func TestTestingSessionPromptPointsAtTheLitmusSkill(t *testing.T) {
	m, _, _, _, _, dataDir := newTestingManager(t, domain.HarnessClaudeCode)
	skill := skillassets.LitmusDir(dataDir) + "/SKILL.md"
	build := func(c context.Context, id domain.SessionID) string {
		t.Helper()
		p, err := m.buildSystemPrompt(c, domain.KindWorker, "mer", id, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	// The spawn path knows only the ctx marker (the run dir is made later); a relaunch finds the run dir.
	if p := build(WithTestingProfile(ctx), "mer-9"); !strings.Contains(p, skill) || strings.Contains(p, skillassets.Dir(dataDir)) {
		t.Fatalf("spawn prompt does not point only at the litmus skill:\n%s", p)
	}
	rec := testingSpawn(t, m)
	if p := build(ctx, rec.ID); !strings.Contains(p, skill) || strings.Contains(p, "ao preview") {
		t.Fatalf("relaunch prompt does not point only at the litmus skill:\n%s", p)
	}
	// A run dir alone does not make a session a tester: any session's `ao testing intake` creates one.
	if err := os.MkdirAll(litmus.RunDir(dataDir, "mer-11"), 0o750); err != nil {
		t.Fatal(err)
	}
	if p := build(ctx, "mer-11"); strings.Contains(p, skill) {
		t.Fatalf("a session with a stray run dir got the litmus pointer:\n%s", p)
	}
	if p := build(ctx, "mer-10"); strings.Contains(p, skill) || !strings.Contains(p, skillassets.Dir(dataDir)) {
		t.Fatalf("an ordinary session lost the using-ao pointer:\n%s", p)
	}
}

func TestTestingSessionRefusesHarnessAndInterfaceChanges(t *testing.T) {
	m, _, _, _, _, _ := newTestingManager(t, domain.HarnessClaudeCode)
	rec := testingSpawn(t, m)
	if _, err := m.SwitchAgent(ctx, rec.ID, SwitchAgentConfig{TargetHarness: domain.HarnessCodex}); !errors.Is(err, ErrTestingSessionFixed) {
		t.Fatalf("SwitchAgent err = %v", err)
	}
	if _, err := m.StartInterfaceTransition(ctx, rec.ID, domain.SessionModeChat, "", ""); !errors.Is(err, ErrTestingSessionFixed) {
		t.Fatalf("StartInterfaceTransition err = %v", err)
	}
	if err := m.refuseTestingSession(ctx, "mer-99"); err != nil { // an ordinary session is not refused here
		t.Fatalf("ordinary session refused: %v", err)
	}
}
