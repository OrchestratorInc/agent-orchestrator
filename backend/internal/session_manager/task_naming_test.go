package sessionmanager

import (
	"context"
	"errors"
	"path/filepath"
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
	gate     chan struct{} // holds the naming answer until a test releases it
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
			wait, stop := m.startTaskNaming(cfg, domain.SessionRecord{ID: "mer-1", Harness: agent.Harness}, st.projects["mer"].Config)
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
	if l.gate != nil {
		select {
		case <-l.gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return l.result, l.err
}

// The agent starts in the checkout it was created in, before naming answers.
// Naming then renames only the branch; the folder never moves.
func TestTaskNamingAfterAgentLaunch(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, spawn := range []struct {
			mode  domain.SessionMode
			async bool
		}{{domain.SessionModeChat, true}, {domain.SessionModeChat, false}, {domain.SessionModeTUI, false}} {
			mode := spawn.mode
			t.Run(string(mode)+"/async="+map[bool]string{false: "false", true: "true"}[spawn.async]+"/prepared="+map[bool]string{false: "false", true: "true"}[prepared], func(t *testing.T) {
				m, st, _, _ := newGitTaskPreparationManager(t)
				m.dataDir = t.TempDir()
				launcher := &taskNamingLauncher{result: `{"title":"Fix login redirect","slug":"fix-login-redirect"}`, gate: make(chan struct{})}
				m.chat = launcher
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
					m.runBackground = func(fn func()) { go fn() }
				}
				rec, _, _, err := m.Spawn(context.Background(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				if spawn.async {
					if len(launcher.started) != 0 || len(work) != 1 {
						t.Fatal("API waited for naming or agent startup")
					}
					m.runBackground = func(fn func()) { go fn() }
					work[0]()
				}
				launched := st.sessions[rec.ID]
				if launched.Metadata.Branch != "ao/"+string(rec.ID)+"/root" || filepath.Base(launched.Metadata.WorkspacePath) != string(rec.ID) {
					t.Fatalf("agent did not launch on the temporary names: %+v", launched)
				}
				if mode == domain.SessionModeChat && (len(launcher.started) != 1 || launcher.started[0].WorkspacePath != launched.Metadata.WorkspacePath) {
					t.Fatalf("agent launch = %+v", launcher.started)
				}
				close(launcher.gate)
				if err := m.WaitBackgroundWorkers(context.Background()); err != nil {
					t.Fatal(err)
				}
				actual := st.sessions[rec.ID]
				if actual.Metadata.Branch != "ao/fix-login-redirect/root" || actual.Metadata.WorkspacePath != launched.Metadata.WorkspacePath || actual.DisplayName != "Fix login redirect" {
					t.Fatalf("named session = %+v", actual)
				}
				if got := strings.TrimSpace(runManagerGit(t, actual.Metadata.WorkspacePath, "branch", "--show-current")); got != actual.Metadata.Branch {
					t.Fatalf("checkout branch = %q", got)
				}
				if len(launcher.background) != 1 || launcher.background[0].Model != "gpt-5.4" || launcher.background[0].Effort != "low" {
					t.Fatalf("naming calls = %+v", launcher.background)
				}
			})
		}
	}
}

// Regression: a speculative worktree that publishes its path after the visible
// row was read used to fail the start ("no longer accepts this workspace").
func TestTaskNamingAfterLatePreparedWorkspace(t *testing.T) {
	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.chat = &taskNamingLauncher{result: `{"title":"Fix login redirect","slug":"fix-login-redirect"}`}
	var work []func()
	m.runBackground = func(fn func()) { work = append(work, fn) }
	token, err := m.PrepareTaskWorkspace(context.Background(), st.projects["mer"])
	if err != nil {
		t.Fatal(err)
	}
	cfg := asyncChatSpawnConfig("Fix login redirect after expiry")
	cfg.NameFromPrompt, cfg.TaskPreparation = true, token
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(work) != 2 {
		t.Fatalf("background work = %d", len(work))
	}
	m.runBackground = func(fn func()) { fn() }
	for _, fn := range work { // the preparation publishes its path after the row was read
		fn()
	}
	actual := st.sessions[rec.ID]
	if actual.ProvisionState != domain.SessionProvisionReady || actual.Metadata.Branch != "ao/fix-login-redirect/root" || filepath.Base(actual.Metadata.WorkspacePath) != string(rec.ID) {
		t.Fatalf("state=%s error=%q branch=%s path=%s", actual.ProvisionState, actual.ProvisionError, actual.Metadata.Branch, actual.Metadata.WorkspacePath)
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
	daemon := m.backgroundContext
	m.backgroundContext = ctx
	wait, stop := m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
	defer stop()
	if got := wait(); got.Slug != "fix-login-redirect" {
		t.Fatalf("deadline fallback = %+v", got)
	}
	if err := m.WaitBackgroundWorkers(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.backgroundContext = daemon
	m.taskNamingSlots = make(chan struct{}, 1)
	m.taskNamingSlots <- struct{}{}
	wait, stop = m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
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

// launchedTaskSession publishes a session as running in a fresh checkout.
func launchedTaskSession(t *testing.T, m *Manager, st *fakeStore, ws ports.Workspace) (domain.SessionRecord, ports.WorkspaceInfo) {
	t.Helper()
	rec, err := st.CreateSession(context.Background(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, DisplayName: "Provisional"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "ao/mer-1/root", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rec.Metadata.Branch, rec.Metadata.WorkspacePath, rec.Metadata.ProviderConversationID = root.Branch, root.Path, "live-provider"
	st.sessions[rec.ID] = rec
	return rec, root
}

func nameNow(name generatedTaskName) func() generatedTaskName {
	return func() generatedTaskName { return name }
}

func TestTaskNamingPreservesManualRename(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	manual := st.sessions[rec.ID]
	manual.DisplayName = "My title"
	st.sessions[rec.ID] = manual
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Title: "Generated title", Slug: "fix-login"}), func() {})
	if actual := st.sessions[rec.ID]; actual.DisplayName != "My title" || actual.Metadata.Branch != "ao/fix-login/root" {
		t.Fatalf("manual title overwritten or branch not named: %+v", actual)
	}
}

// AO attributes PRs by the session branch namespace, so once the agent has made
// a sibling branch under the temporary name, naming keeps that name.
func TestTaskNamingKeepsBranchTheAgentBuiltOn(t *testing.T) {
	m, st, ws, repo := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	runManagerGit(t, repo, "branch", "ao/mer-1/fix-tests")
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Title: "Fix login", Slug: "fix-login"}), func() {})
	actual := st.sessions[rec.ID]
	if actual.Metadata.Branch != root.Branch || actual.DisplayName != "Fix login" {
		t.Fatalf("naming after agent branched = %+v", actual)
	}
	if got := strings.TrimSpace(runManagerGit(t, root.Path, "branch", "--show-current")); got != root.Branch {
		t.Fatalf("checkout branch = %q", got)
	}
}

type taskBranchMovedStore struct{ *fakeStore }

func (s taskBranchMovedStore) SetSessionTaskBranch(context.Context, domain.SessionID, string, string, time.Time) (bool, error) {
	return false, nil
}

// Git and the row must agree: when the row no longer carries the temporary
// branch, the Git rename is put back.
func TestTaskNamingUndoesRenameTheRowRejects(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	m.store = taskBranchMovedStore{st}
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Slug: "fix-login"}), func() {})
	if got := strings.TrimSpace(runManagerGit(t, root.Path, "branch", "--show-current")); got != root.Branch || st.sessions[rec.ID].Metadata.Branch != root.Branch {
		t.Fatalf("git branch = %q, row = %+v", got, st.sessions[rec.ID].Metadata)
	}
}

func TestTaskNamingRenamesEveryRepository(t *testing.T) {
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
	if actual.Metadata.Branch != "ao/fix-login-redirect/root" || filepath.Base(actual.Metadata.WorkspacePath) != string(rec.ID) {
		t.Fatalf("named session = %+v", actual)
	}
	if rows := st.worktrees[rec.ID]; len(rows) != 2 {
		t.Fatalf("worktree rows = %+v", rows)
	} else {
		for _, row := range rows {
			if row.Branch != actual.Metadata.Branch || !strings.HasPrefix(row.WorktreePath, actual.Metadata.WorkspacePath) {
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

func TestTaskNamingRetryPlanRetainsCheckpointOnce(t *testing.T) {
	in := asyncChatSpawn{retry: true, cfg: ports.SpawnConfig{NameFromPrompt: true}, record: domain.SessionRecord{ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepFetch, Status: domain.SessionProvisionStepDone}, {ID: domain.SessionProvisionStepWorktree}, {ID: domain.SessionProvisionStepSetup}, {ID: domain.SessionProvisionStepAgent}}}}
	plan := provisionPlan(in, false)
	if len(plan) != 3 || plan[0] != domain.SessionProvisionStepWorktree || plan[1] != domain.SessionProvisionStepSetup || plan[2] != domain.SessionProvisionStepAgent {
		t.Fatalf("retry plan = %v", plan)
	}
}
