package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	chatregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type taskNamingLauncher struct {
	recordingLauncher
	result   string
	err      error
	wait     bool
	onNaming func()
}

func TestTaskNamingAllShippedAgents(t *testing.T) {
	drivers := chatregistry.Build(nil, nil)
	for _, agent := range agentregistry.Harnessed() {
		t.Run(string(agent.Harness), func(t *testing.T) {
			launcher := &taskNamingLauncher{result: `{"title":"Fix login redirect","slug":"login-redirect"}`}
			want := "login-redirect"
			if !drivers.SupportsChat(agent.Harness) {
				launcher.err = ports.ErrChatUnsupported
				want = "fix-login-redirect"
			}
			m, st, _ := newChatManager(launcher)
			m.dataDir = t.TempDir()
			m.runBackground = func(work func()) { work() }
			cfg := asyncChatSpawnConfig("Fix login redirect")
			cfg.Harness, cfg.NameFromPrompt, cfg.AgentConfigResolved = agent.Harness, true, true
			cfg.AgentConfig.Model = "selected-model"
			wait, stop := m.startTaskNaming(t.Context(), cfg, domain.SessionRecord{ID: "mer-1", Harness: agent.Harness}, st.projects["mer"].Config)
			defer stop()
			if got := wait(); got.Slug != want {
				t.Fatalf("name = %+v, want %q", got, want)
			}
			if len(launcher.backgroundHarnesses) != 1 || launcher.backgroundHarnesses[0] != agent.Harness || launcher.background[0].Model != "selected-model" {
				t.Fatalf("selected agent/model not preserved: %v, %+v", launcher.backgroundHarnesses, launcher.background)
			}
		})
	}
}

type taskNamingSaveFailureStore struct{ *fakeStore }

func (s taskNamingSaveFailureStore) SetSessionNamedWorkspace(context.Context, domain.SessionID, string, string, string, string, time.Time) (bool, error) {
	return false, errors.New("simulated database interruption")
}

func TestTaskNamingRestartRecoversBeforeDatabaseSave(t *testing.T) {
	for _, spawn := range []struct {
		mode  domain.SessionMode
		state domain.SessionProvisionState
	}{{domain.SessionModeChat, domain.SessionProvisionProvisioning}, {domain.SessionModeTUI, domain.SessionProvisionProvisioning}, {domain.SessionModeChat, domain.SessionProvisionReady}, {domain.SessionModeTUI, domain.SessionProvisionReady}} {
		t.Run(string(spawn.mode)+"/"+string(spawn.state), func(t *testing.T) {
			m, st, ws, _ := newGitTaskPreparationManager(t)
			launcher := &taskNamingLauncher{}
			m.chat = launcher
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: spawn.mode, ProvisionState: spawn.state, ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepWorktree, Status: domain.SessionProvisionStepRunning}}})
			if err != nil {
				t.Fatal(err)
			}
			root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/mer-1/root", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			m.store = taskNamingSaveFailureStore{st}
			moved, _, err := m.nameTaskWorkspace(t.Context(), rec, root, nil, generatedTaskName{Slug: "login-redirect"})
			if err == nil || moved.Path == root.Path || st.sessions[rec.ID].Metadata.WorkspacePath != "" {
				t.Fatalf("missing simulated interruption: %+v, %v", moved, err)
			}
			m.store = st
			if err := m.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			actual := st.sessions[rec.ID]
			if actual.Metadata.WorkspacePath != moved.Path || actual.Metadata.Branch != "ao/login-redirect/root" || len(launcher.started) != 0 {
				t.Fatalf("startup recovery = %+v", actual)
			}
			if spawn.state == domain.SessionProvisionReady && actual.ProvisionSteps[0].Status != domain.SessionProvisionStepDone {
				t.Fatalf("repaired synchronous worktree still pending: %+v", actual.ProvisionSteps)
			}
			if _, found, err := ws.RecoverTaskWorkspace(t.Context(), "mer", rec.ID); found || err != nil {
				t.Fatalf("saved recovery was not acknowledged: %v, %v", found, err)
			}
		})
	}
}

func TestTaskNamingRetainsJournalUntilWorktreeProgressIsSaved(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, ProvisionState: domain.SessionProvisionReady, ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepWorktree, Status: domain.SessionProvisionStepRunning}, {ID: domain.SessionProvisionStepSetup, Status: domain.SessionProvisionStepRunning}}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/mer-1/root", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	m.store = namingProgressFailureStore{st}
	_, _, err = m.nameTaskWorkspace(t.Context(), rec, root, nil, generatedTaskName{Slug: "login-redirect"})
	if err == nil || st.sessions[rec.ID].ProvisionSteps[0].Status != domain.SessionProvisionStepRunning {
		t.Fatalf("completion ignored a failed checkpoint: err=%v steps=%+v", err, st.sessions[rec.ID].ProvisionSteps)
	}
	m.store = st
	_, recovered, err := m.recoverTaskNaming(t.Context(), st.sessions[rec.ID])
	if err != nil || !recovered {
		t.Fatalf("checkpoint recovery: found=%v err=%v", recovered, err)
	}
	steps := st.sessions[rec.ID].ProvisionSteps
	if steps[0].Status != domain.SessionProvisionStepDone || steps[1].Status != domain.SessionProvisionStepRunning {
		t.Fatalf("worktree completion changed setup checkpoint: %+v", steps)
	}
	if _, found, err := ws.RecoverTaskWorkspace(t.Context(), rec.ProjectID, rec.ID); found || err != nil {
		t.Fatalf("completed journal still exists: found=%v err=%v", found, err)
	}
}

func (l *taskNamingLauncher) RunBackgroundTask(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
	l.background = append(l.background, cfg)
	l.backgroundHarnesses = append(l.backgroundHarnesses, harness)
	l.backgroundPrompts = append(l.backgroundPrompts, prompt)
	if l.onNaming != nil {
		l.onNaming()
	}
	if l.wait {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return l.result, l.err
}

func TestTaskNamingBeforeAgentLaunch(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, spawn := range []struct {
			mode  domain.SessionMode
			async bool
		}{{domain.SessionModeChat, true}, {domain.SessionModeChat, false}, {domain.SessionModeTUI, false}} {
			mode := spawn.mode
			t.Run(string(mode)+"/async="+map[bool]string{false: "false", true: "true"}[spawn.async]+"/prepared="+map[bool]string{false: "false", true: "true"}[prepared], func(t *testing.T) {
				m, st, _, _ := newGitTaskPreparationManager(t)
				m.dataDir = t.TempDir()
				launcher := &taskNamingLauncher{result: `{"title":"Fix login redirect","slug":"fix-login-redirect"}`}
				m.chat = launcher
				launcher.onNaming = func() {
					for _, pending := range st.sessions {
						if pending.IsTaskPreparation || pending.ProvisionState == domain.SessionProvisionProvisioning {
							continue
						}
						unfinished := false
						for _, step := range pending.ProvisionSteps {
							unfinished = unfinished || step.ID == domain.SessionProvisionStepWorktree && step.Status != domain.SessionProvisionStepDone
						}
						if !unfinished {
							t.Fatalf("naming worker has no pending worktree progress: %+v", pending)
						}
					}
				}
				var token domain.TaskPreparationToken
				var err error
				if prepared {
					token, err = m.PrepareTaskWorkspace(context.Background(), st.projects["mer"])
					if err != nil {
						t.Fatal(err)
					}
				}
				cfg := asyncChatSpawnConfig("Fix login redirect after expiry")
				cfg.RequestedMode, cfg.NameFromPrompt, cfg.TaskPreparation, cfg.Async = mode, true, token, spawn.async
				cfg.DisplayName = "Fix login redirect after expiry"
				cfg.AgentConfig.Model, cfg.AgentConfig.Effort = "gpt-5.4", "low"
				var work []func()
				m.runBackground = func(fn func()) { work = append(work, fn) }
				if !spawn.async {
					m.runBackground = func(fn func()) { fn() }
				}
				rec, _, _, err := m.Spawn(context.Background(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				if spawn.async {
					if len(launcher.background) != 0 || len(launcher.started) != 0 || len(work) != 1 {
						t.Fatal("API waited for naming or agent startup")
					}
					m.runBackground = func(fn func()) { fn() }
					work[0]()
				}
				actual := st.sessions[rec.ID]
				if actual.Metadata.Branch != "ao/fix-login-redirect/root" || filepath.Base(actual.Metadata.WorkspacePath) != "fix-login-redirect" || actual.DisplayName != "Fix login redirect" {
					t.Fatalf("named session = %+v", actual)
				}
				worktreeDone := false
				for _, step := range actual.ProvisionSteps {
					worktreeDone = worktreeDone || step.ID == domain.SessionProvisionStepWorktree && step.Status == domain.SessionProvisionStepDone
				}
				if !worktreeDone {
					t.Fatalf("named worker still has unfinished worktree progress: %+v", actual.ProvisionSteps)
				}
				if len(launcher.background) != 1 || launcher.background[0].Model != "gpt-5.4" || launcher.background[0].Effort != "low" {
					t.Fatalf("naming calls = %+v", launcher.background)
				}
				if mode == domain.SessionModeChat && launcher.started[0].WorkspacePath != actual.Metadata.WorkspacePath {
					t.Fatal("agent launched in temporary workspace")
				}
			})
		}
	}
}

func TestTaskNamingFallbackAndManualTitle(t *testing.T) {
	for _, tt := range []struct {
		name      string
		result    string
		err       error
		slug      string
		keepTitle bool
	}{
		{name: "generated slug keeps explicit title", result: `{"title":"Replace this title","slug":"login-redirect"}`, slug: "login-redirect", keepTitle: true},
		{name: "provider error", err: errors.New("offline")},
		{name: "malformed response", result: "Here is a title"},
		{name: "unusable response", result: `{"title":"...","slug":"你好"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, st, _, _ := newGitTaskPreparationManager(t)
			m.dataDir = t.TempDir()
			m.chat = &taskNamingLauncher{result: tt.result, err: tt.err}
			cfg := asyncChatSpawnConfig("Fix login redirect")
			cfg.NameFromPrompt, cfg.Async, cfg.DisplayName = true, false, "My manual title"
			cfg.KeepDisplayName = tt.keepTitle
			rec, _, _, err := m.Spawn(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			slug := tt.slug
			if slug == "" {
				slug = "fix-login-redirect"
			}
			if actual := st.sessions[rec.ID]; actual.Metadata.Branch != "ao/"+slug+"/root" || actual.DisplayName != "My manual title" {
				t.Fatalf("fallback = %+v", actual)
			}
		})
	}
}

func TestTaskNamingDeadlineAndCapacity(t *testing.T) {
	m, st, _ := newChatManager(&taskNamingLauncher{wait: true})
	m.dataDir = t.TempDir()
	cfg := asyncChatSpawnConfig("Fix login redirect")
	cfg.NameFromPrompt = true
	rec := domain.SessionRecord{ID: "mer-1", Harness: cfg.Harness}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	wait, stop := m.startTaskNaming(ctx, cfg, rec, st.projects["mer"].Config)
	defer stop()
	if got := wait(); got.Slug != "fix-login-redirect" {
		t.Fatalf("deadline fallback = %+v", got)
	}
	if err := m.WaitBackgroundWorkers(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.taskNamingSlots = make(chan struct{}, 1)
	m.taskNamingSlots <- struct{}{}
	wait, stop = m.startTaskNaming(context.Background(), cfg, rec, st.projects["mer"].Config)
	defer stop()
	if got := wait(); got.Slug != "fix-login-redirect" {
		t.Fatalf("capacity fallback = %+v", got)
	}
}

func TestParseTaskName(t *testing.T) {
	fallback := generatedTaskName{Slug: "fallback"}
	for _, tt := range []struct{ raw, slug string }{
		{`{"title":"Fix redirect","slug":"Fix / Login..Redirect"}`, "fix-login-redirect"},
		{"```json\n{\"title\":\"Fix redirect\",\"slug\":\"fix-login\"}\n```", "fix-login"},
		{`{"slug":"../../"}`, "fallback"},
		{`{"slug":"one two three four five six seven"}`, "one-two-three"},
		{`{"slug":"` + strings.Repeat("a", 100) + `"}`, strings.Repeat("a", 24)},
		{`{"slug":"vaporwave-site-redesign"}`, "vaporwave-site-redesign"},
		{`broken`, "fallback"},
	} {
		if got := parseTaskName(tt.raw, fallback); got.Slug != tt.slug {
			t.Errorf("parse %q = %+v, want %q", tt.raw, got, tt.slug)
		}
	}
}

func TestTaskNamingPreservesManualRename(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, DisplayName: "Provisional"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/mer-1/root", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	manual := rec
	manual.DisplayName = "My title"
	st.sessions[rec.ID] = manual
	_, _, err = m.nameTaskWorkspace(context.Background(), rec, root, nil, generatedTaskName{Title: "Generated title", Slug: "fix-login"})
	if err != nil || st.sessions[rec.ID].DisplayName != "My title" {
		t.Fatalf("manual title overwritten: %+v, %v", st.sessions[rec.ID], err)
	}
}

func TestTaskNamingPersistsMultiRepoPaths(t *testing.T) {
	m, st, _, repo := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	child := newManagerGitRepo(t)
	runManagerGit(t, repo, "clone", child, "child")
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	st.projects["mer"] = project
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "child", RelativePath: "child", DefaultBranch: "main"}}
	m.chat = &taskNamingLauncher{result: `{"title":"Fix login redirect","slug":"fix-login-redirect"}`}
	token, err := m.PrepareTaskWorkspace(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	cfg := asyncChatSpawnConfig("Fix login redirect")
	cfg.Async, cfg.NameFromPrompt, cfg.TaskPreparation = false, true, token
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	actual := st.sessions[rec.ID]
	if rows := st.worktrees[rec.ID]; len(rows) != 2 {
		t.Fatalf("worktree rows = %+v", rows)
	} else {
		for _, row := range rows {
			if row.Branch != "ao/fix-login-redirect/root" || !strings.HasPrefix(row.WorktreePath, actual.Metadata.WorkspacePath) {
				t.Fatalf("stale durable worktree row: %+v", row)
			}
			if got := strings.TrimSpace(runManagerGit(t, row.WorktreePath, "branch", "--show-current")); got != row.Branch {
				t.Fatalf("actual branch = %q, durable = %q", got, row.Branch)
			}
		}
	}
}

func BenchmarkTaskNameParsing(b *testing.B) {
	fallback := generatedTaskName{Slug: "fix-login-redirect"}
	for b.Loop() {
		parseTaskName(`{"title":"Preserve destination after login","slug":"fix-login-redirect"}`, fallback)
	}
}

// A failed rename can still have moved the checkout. Retry must finish the
// durable stages, not treat the recovery path as a setup-complete marker.
type namingRepairTestWorkspace struct {
	ports.Workspace
	namer   ports.WorkspaceTaskNamer
	repairs int
}

func (w *namingRepairTestWorkspace) NameTaskWorkspace(ctx context.Context, info ports.WorkspaceProjectInfo, branch, name string) (ports.WorkspaceProjectInfo, error) {
	info, err := w.namer.NameTaskWorkspace(ctx, info, branch, name)
	if err == nil {
		err = errors.New("interrupted after moving checkout")
	}
	return info, err
}
func (w *namingRepairTestWorkspace) RepairTaskWorkspace(ctx context.Context, info ports.WorkspaceProjectInfo) (ports.WorkspaceProjectInfo, error) {
	w.repairs++
	return w.namer.RepairTaskWorkspace(ctx, info)
}
func (w *namingRepairTestWorkspace) RecoverTaskWorkspace(ctx context.Context, projectID domain.ProjectID, id domain.SessionID) (ports.WorkspaceProjectInfo, bool, error) {
	return w.Workspace.(ports.WorkspaceTaskNamingRecovery).RecoverTaskWorkspace(ctx, projectID, id)
}
func (w *namingRepairTestWorkspace) CompleteTaskWorkspaceNaming(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	return w.Workspace.(ports.WorkspaceTaskNamingRecovery).CompleteTaskWorkspaceNaming(ctx, info)
}
func TestTaskNamingRetryFinishesWorkspaceSetup(t *testing.T) {
	for _, scenario := range []string{"repair_and_setup", "setup_only", "unsaved_path"} {
		t.Run(scenario, func(t *testing.T) {
			worktreeDone := scenario == "setup_only"
			m, st, _, _ := newGitTaskPreparationManager(t)
			m.dataDir = t.TempDir()
			launcher := &taskNamingLauncher{result: `{"title":"Fix login","slug":"fix-login"}`}
			m.chat = launcher
			ws := &namingRepairTestWorkspace{Workspace: m.workspace, namer: m.workspace.(ports.WorkspaceTaskNamer)}
			m.workspace = ws
			project := st.projects["mer"]
			command := "printf x >> setup-runs"
			if runtime.GOOS == "windows" {
				command = "echo x>> setup-runs"
			}
			project.Config.PostCreate = []string{command}
			st.projects["mer"] = project
			var work []func()
			m.runBackground = func(fn func()) { work = append(work, fn) }
			cfg := asyncChatSpawnConfig("Fix login")
			cfg.NameFromPrompt = true
			rec, _, _, err := m.Spawn(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			m.runBackground = func(fn func()) { fn() }
			work[0]()
			failed := st.sessions[rec.ID]
			if failed.ProvisionState != domain.SessionProvisionFailed || len(launcher.started) != 0 {
				t.Fatalf("first start = %+v", failed)
			}
			path, branch := failed.Metadata.WorkspacePath, failed.Metadata.Branch
			if path == "" || branch != "ao/fix-login/root" {
				t.Fatalf("missing recovery names: %+v", failed)
			}
			if _, err := os.Stat(filepath.Join(path, "setup-runs")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("setup ran before retry: %v", err)
			}
			if worktreeDone {
				for i := range failed.ProvisionSteps {
					if failed.ProvisionSteps[i].ID == domain.SessionProvisionStepWorktree {
						failed.ProvisionSteps[i].Status = domain.SessionProvisionStepDone
					}
					if failed.ProvisionSteps[i].ID == domain.SessionProvisionStepSetup {
						failed.ProvisionSteps[i].Status = domain.SessionProvisionStepRunning
					}
				}
				st.sessions[rec.ID] = failed
			}
			if scenario == "unsaved_path" {
				failed.Metadata.WorkspacePath, failed.Metadata.Branch = "", ""
				st.sessions[rec.ID] = failed
			}
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
				t.Fatal(err)
			}
			actual := st.sessions[rec.ID]
			if actual.ProvisionState != domain.SessionProvisionReady || actual.Metadata.WorkspacePath != path || actual.Metadata.Branch != branch || len(launcher.started) != 1 {
				t.Fatalf("retry = %+v", actual)
			}
			wantRepairs := 1
			if worktreeDone {
				wantRepairs = 0
			}
			if ws.repairs != wantRepairs {
				t.Fatalf("repairs=%d, want%d", ws.repairs, wantRepairs)
			}
			for _, step := range actual.ProvisionSteps {
				if step.Status != domain.SessionProvisionStepDone {
					t.Fatalf("unfinished retry stage: %+v", step)
				}
			}
			if body, err := os.ReadFile(filepath.Join(path, "setup-runs")); err != nil || strings.TrimSpace(string(body)) != "x" {
				t.Fatalf("setup after retry=%q,%v", body, err)
			}
		})
	}
}

type namingProgressFailureStore struct{ *fakeStore }

func (s namingProgressFailureStore) SetSessionProvisionSteps(context.Context, domain.SessionID, []domain.SessionProvisionStep, time.Time) error {
	return errors.New("cannot persist naming checkpoint")
}
func TestTaskNamingRequiresDurableRecoveryCheckpoint(t *testing.T) {
	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.store = namingProgressFailureStore{st}
	launcher := &taskNamingLauncher{result: `{"title":"Fix login","slug":"fix-login"}`}
	m.chat = launcher
	var work []func()
	m.runBackground = func(fn func()) { work = append(work, fn) }
	cfg := asyncChatSpawnConfig("Fix login")
	cfg.NameFromPrompt = true
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.runBackground = func(fn func()) { fn() }
	work[0]()
	actual := st.sessions[rec.ID]
	if actual.ProvisionState != domain.SessionProvisionFailed || actual.Metadata.WorkspacePath != "" || len(launcher.started) != 0 {
		t.Fatalf("uncheckpointed naming published workspace: %+v", actual)
	}
}

func TestTaskNamingRecoverySkipsLaunchedAgent(t *testing.T) {
	for _, identity := range []string{"runtime", "provider", "resume"} {
		t.Run(identity, func(t *testing.T) {
			m, st, ws, _ := newGitTaskPreparationManager(t)
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, ProvisionState: domain.SessionProvisionFailed})
			if err != nil {
				t.Fatal(err)
			}
			root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/mer-1/root", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			m.store = taskNamingSaveFailureStore{st}
			if _, _, err := m.nameTaskWorkspace(t.Context(), rec, root, nil, generatedTaskName{Slug: "login-redirect"}); err == nil {
				t.Fatal("expected unsaved journal")
			}
			m.store = st
			switch identity {
			case "runtime":
				rec.Metadata.RuntimeHandleID = "active-runtime"
			case "provider":
				rec.Metadata.ProviderConversationID = "active-conversation"
			case "resume":
				if err := m.beginAgentResume(t.Context(), rec.ID); err != nil {
					t.Fatal(err)
				}
				defer m.endAgentResume(rec.ID)
			}
			st.sessions[rec.ID] = rec
			if err := m.ReconcileBackground(t.Context()); err != nil {
				t.Fatal(err)
			}
			if st.sessions[rec.ID].Metadata.WorkspacePath != "" {
				t.Fatal("recovered checkout in use by agent")
			}
			if _, found, err := ws.RecoverTaskWorkspace(t.Context(), "mer", rec.ID); !found || err != nil {
				t.Fatalf("recovery intent lost: %v, %v", found, err)
			}
		})
	}
}

func TestTaskNamingRetryPlanRetainsCheckpointOnce(t *testing.T) {
	in := asyncChatSpawn{retry: true, cfg: ports.SpawnConfig{NameFromPrompt: true}, record: domain.SessionRecord{ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepFetch, Status: domain.SessionProvisionStepDone}, {ID: domain.SessionProvisionStepWorktree}, {ID: domain.SessionProvisionStepSetup}, {ID: domain.SessionProvisionStepAgent}}}}
	plan := provisionPlan(in, false)
	if len(plan) != 3 || plan[0] != domain.SessionProvisionStepWorktree || plan[1] != domain.SessionProvisionStepSetup || plan[2] != domain.SessionProvisionStepAgent {
		t.Fatalf("retry plan = %v", plan)
	}
}
