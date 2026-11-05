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

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	chatregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type taskNamingLauncher struct {
	recordingLauncher
	result   string
	err      error
	wait     bool
	gate     chan struct{} // holds the naming answer until a test releases it
	onNaming func()
	onDrain  func()
}

func (l *taskNamingLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	if l.onDrain != nil {
		l.onDrain()
	}
	return l.recordingLauncher.DrainChatQueue(ctx, id)
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

// A send while the prepared worktree is still being created reads the session
// before preparation publishes its workspace; naming must still name it.
func TestTaskNamingAcceptsWorkspacePublishedAfterRead(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	stale := rec
	stale.Metadata.Branch, stale.Metadata.WorkspacePath = "", ""
	m.nameTaskBranchLater(context.Background(), stale, root, nil, nameNow(generatedTaskName{Slug: "login-redirect"}), func() {})
	if actual := st.sessions[rec.ID]; actual.Metadata.Branch != "ao/login-redirect/root" || actual.Metadata.WorkspacePath != root.Path {
		t.Fatalf("naming refused a workspace published after the read: %+v", actual.Metadata)
	}
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
// A sibling the agent made under the temporary name stays attributed through
// that namespace, so the rename still gives the task its semantic branch.
func TestTaskNamingRenamesBesideAgentBranches(t *testing.T) {
	m, st, ws, repo := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	runManagerGit(t, repo, "branch", "ao/mer-1/fix-tests")
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Title: "Fix login", Slug: "fix-login"}), func() {})
	actual := st.sessions[rec.ID]
	if actual.Metadata.Branch != "ao/fix-login/root" || actual.DisplayName != "Fix login" {
		t.Fatalf("naming after agent branched = %+v", actual)
	}
	if got := strings.TrimSpace(runManagerGit(t, repo, "branch", "--list", "ao/mer-1/fix-tests")); got != "ao/mer-1/fix-tests" {
		t.Fatalf("agent branch = %q", got)
	}
}

type namingWritesStore struct {
	*fakeStore
	writes chan bool
}

func (s *namingWritesStore) SetSessionBranchNaming(ctx context.Context, id domain.SessionID, naming bool) error {
	s.writes <- naming
	return s.fakeStore.SetSessionBranchNaming(ctx, id, naming)
}

// A project gate held past the rename deadline by another start's setup must
// not drop the rename or the generated title, nor hide the branch meanwhile.
func TestTaskNamingWaitsForGateBeforeDeadline(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	defer func(d time.Duration) { taskBranchRenameTimeout = d }(taskBranchRenameTimeout)
	taskBranchRenameTimeout = time.Second
	store := &namingWritesStore{fakeStore: st, writes: make(chan bool, 1)}
	m.store = store
	release := m.acquireWorkspaceGate(rec.ProjectID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Title: "Fix login", Slug: "fix-login"}), func() {})
	}()
	if naming := <-store.writes; naming {
		t.Fatal("waiting on the gate kept the branch hidden")
	}
	time.Sleep(taskBranchRenameTimeout + 100*time.Millisecond)
	release()
	<-done
	if actual := st.sessions[rec.ID]; actual.Metadata.Branch != "ao/fix-login/root" || actual.DisplayName != "Fix login" {
		t.Fatalf("after a long gate wait = %+v", actual)
	}
}

// Clients keep the temporary branch private from the start of naming until
// the rename lands, and a start that fails shows it again.
func TestTaskNamingBranchNamingFlag(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	m.chat = &taskNamingLauncher{result: `{"title":"Fix login","slug":"fix-login"}`}
	rec, root := launchedTaskSession(t, m, st, ws)
	cfg := asyncChatSpawnConfig("Fix login")
	cfg.NameFromPrompt = true
	taskName, cancel := m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
	if !st.sessions[rec.ID].BranchNaming {
		t.Fatal("naming did not hide the temporary branch")
	}
	m.nameTaskBranchLater(context.Background(), rec, root, nil, taskName, cancel)
	if actual := st.sessions[rec.ID]; actual.BranchNaming || actual.Metadata.Branch != "ao/fix-login/root" {
		t.Fatalf("after the rename = %+v", actual)
	}
	_, cancel = m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
	cancel()
	if st.sessions[rec.ID].BranchNaming {
		t.Fatal("a failed start left the branch hidden")
	}
}

// A start that fails after launch keeps its temporary branch, and the project
// gate is free once the agent is launched.
func TestAsyncStartNamesOnlyAfterSuccess(t *testing.T) {
	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	launcher := &taskNamingLauncher{result: `{"title":"Fix login","slug":"fix-login"}`}
	launcher.drainErr = errors.New("controller stopped before dispatch")
	gateFree := false
	launcher.onDrain = func() {
		if gate := m.workspaceGate("mer"); gate.TryLock() {
			gateFree = true
			gate.Unlock()
		}
	}
	m.chat = launcher
	cfg := asyncChatSpawnConfig("Fix login")
	cfg.NameFromPrompt = true
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	actual := st.sessions[rec.ID]
	if !gateFree || actual.ProvisionState != domain.SessionProvisionFailed || actual.Metadata.Branch != "ao/"+string(rec.ID)+"/root" || actual.BranchNaming {
		t.Fatalf("gate free = %v, session = %+v", gateFree, actual)
	}
}

// A generated name equal to the temporary one is not a collision.
func TestTaskNamingKeepsMatchingTemporaryName(t *testing.T) {
	m, st, ws, _ := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Slug: taskSlug(string(rec.ID))}), func() {})
	if got := st.sessions[rec.ID].Metadata.Branch; got != root.Branch {
		t.Fatalf("branch = %q, want %q", got, root.Branch)
	}
}

type taskBranchMovedStore struct {
	*fakeStore
	repo string
	t    *testing.T
}

// SetSessionTaskBranch rejects the rename after the agent made a ref under the
// temporary namespace.
func (s taskBranchMovedStore) SetSessionTaskBranch(_ context.Context, _ domain.SessionID, expected, _ string, _ map[string]string, _ time.Time) (bool, error) {
	runManagerGit(s.t, s.repo, "branch", strings.TrimSuffix(expected, "/root")+"/fix")
	return false, nil
}

// Git and the row must agree: when the row no longer carries the temporary
// branch, the Git rename is put back exactly, past the agent's new ref.
func TestTaskNamingUndoesRenameTheRowRejects(t *testing.T) {
	m, st, ws, repo := newGitTaskPreparationManager(t)
	rec, root := launchedTaskSession(t, m, st, ws)
	m.store = taskBranchMovedStore{st, repo, t}
	m.nameTaskBranchLater(context.Background(), rec, root, nil, nameNow(generatedTaskName{Slug: "fix-login"}), func() {})
	if got := strings.TrimSpace(runManagerGit(t, root.Path, "branch", "--show-current")); got != root.Branch || st.sessions[rec.ID].Metadata.Branch != root.Branch {
		t.Fatalf("git branch = %q, row = %+v", got, st.sessions[rec.ID].Metadata)
	}
}

type flakyTaskBranchStore struct {
	Store
	failures int
}

func (s *flakyTaskBranchStore) SetSessionTaskBranch(ctx context.Context, id domain.SessionID, expected, branch string, repos map[string]string, now time.Time) (bool, error) {
	if s.failures > 0 {
		s.failures--
		return false, errors.New("database is locked")
	}
	return s.Store.SetSessionTaskBranch(ctx, id, expected, branch, repos, now)
}

type scriptedTaskNamer struct {
	renamed, reverted    ports.WorkspaceProjectInfo
	renameErr, revertErr error
}

func (n scriptedTaskNamer) RenameTaskBranch(context.Context, ports.WorkspaceProjectInfo, string) (ports.WorkspaceProjectInfo, error) {
	return n.renamed, n.renameErr
}

func (n scriptedTaskNamer) RevertTaskBranch(context.Context, ports.WorkspaceProjectInfo, ports.WorkspaceProjectInfo) (ports.WorkspaceProjectInfo, error) {
	return n.reverted, n.revertErr
}

// When a rename or its undo fails, each repository row records the branch its
// checkout holds, and the session row the root's.
func TestTaskNamingRecordsSplitBranches(t *testing.T) {
	locked := errors.New("ref locked")
	for _, tc := range []struct {
		name                    string
		failedWrites            int
		renamed, reverted, held [2]bool // whether the root and the child carry the generated name
		renameErr, revertErr    error
	}{
		{"undo failed", 1, [2]bool{true, true}, [2]bool{true, true}, [2]bool{true, true}, nil, locked},
		{"undo stopped after the root", 1, [2]bool{true, true}, [2]bool{false, true}, [2]bool{false, true}, nil, locked},
		{"rename and undo stopped after the root", 0, [2]bool{true, false}, [2]bool{}, [2]bool{true, false}, locked, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m, fake, _ := newChatManager(&recordingLauncher{})
			st := sqlitetest.MustOpen(t)
			if err := st.UpsertProject(ctx, fake.projects["mer"]); err != nil {
				t.Fatal(err)
			}
			rec, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker})
			if err != nil {
				t.Fatal(err)
			}
			temporary := "ao/" + string(rec.ID)
			split := func(named [2]bool) ports.WorkspaceProjectInfo {
				branch := [2]string{temporary, temporary}
				for i := range named {
					if named[i] {
						branch[i] = "ao/fix-login/root"
					}
				}
				return ports.WorkspaceProjectInfo{
					Root: ports.WorkspaceInfo{ProjectID: "mer", SessionID: rec.ID, Path: "/w/root", Branch: branch[0]},
					Worktrees: []ports.WorkspaceRepoInfo{
						{RepoName: domain.RootWorkspaceRepoName, Path: "/w/root", Branch: branch[0]},
						{RepoName: "child", Path: "/w/root/child", Branch: branch[1]},
					},
				}
			}
			original := split([2]bool{})
			rec.Metadata.Branch, rec.Metadata.WorkspacePath = temporary, original.Root.Path
			if err := st.UpdateSession(ctx, rec); err != nil {
				t.Fatal(err)
			}
			for _, wt := range original.Worktrees {
				if err := st.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{SessionID: rec.ID, RepoName: wt.RepoName, Branch: wt.Branch, WorktreePath: wt.Path, State: "active"}); err != nil {
					t.Fatal(err)
				}
			}
			m.store = &flakyTaskBranchStore{Store: st, failures: tc.failedWrites}
			namer := scriptedTaskNamer{renamed: split(tc.renamed), reverted: split(tc.reverted), renameErr: tc.renameErr, revertErr: tc.revertErr}
			if _, err := m.renameTaskBranch(namer, rec, original.Root, &original, "fix-login"); err == nil {
				t.Fatal("failure not reported")
			}
			want := split(tc.held)
			if got, _, _ := st.GetSession(ctx, rec.ID); got.Metadata.Branch != want.Root.Branch {
				t.Fatalf("session branch = %q, want %q", got.Metadata.Branch, want.Root.Branch)
			}
			rows, err := st.ListSessionWorktrees(ctx, rec.ID)
			if err != nil || len(rows) != 2 {
				t.Fatalf("rows = %+v, %v", rows, err)
			}
			for _, row := range rows {
				i := slices.IndexFunc(want.Worktrees, func(wt ports.WorkspaceRepoInfo) bool { return wt.RepoName == row.RepoName })
				if row.Branch != want.Worktrees[i].Branch {
					t.Fatalf("%s row = %q, Git holds %q", row.RepoName, row.Branch, want.Worktrees[i].Branch)
				}
			}
		})
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

// A failed start whose saved worktree no longer exists (removed by hand, or
// left mid-move by an older build) starts again in a fresh one on retry.
func TestRetryRecreatesMissingWorktree(t *testing.T) {
	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.chat = &taskNamingLauncher{}
	rec, _, _, err := m.Spawn(context.Background(), asyncChatSpawnConfig("Fix login"))
	if err != nil {
		t.Fatal(err)
	}
	failed := st.sessions[rec.ID]
	if failed.Metadata.WorkspacePath == "" {
		t.Fatalf("start published no worktree: %+v", failed)
	}
	failed.ProvisionState, failed.Metadata.ProviderConversationID = domain.SessionProvisionFailed, ""
	failed.ProvisionSteps = []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepWorktree, Status: domain.SessionProvisionStepDone}, {ID: domain.SessionProvisionStepAgent, Status: domain.SessionProvisionStepRunning}}
	st.sessions[rec.ID] = failed
	if err := os.RemoveAll(failed.Metadata.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	actual := st.sessions[rec.ID]
	if _, err := os.Stat(actual.Metadata.WorkspacePath); actual.ProvisionState != domain.SessionProvisionReady || err != nil {
		t.Fatalf("retry = state %s error %q path %s, worktree: %v", actual.ProvisionState, actual.ProvisionError, actual.Metadata.WorkspacePath, err)
	}
	// The checklist shows the worktree it created again.
	if !slices.ContainsFunc(actual.ProvisionSteps, func(step domain.SessionProvisionStep) bool {
		return step.ID == domain.SessionProvisionStepWorktree && step.Status == domain.SessionProvisionStepDone
	}) {
		t.Fatalf("retry checklist = %+v", actual.ProvisionSteps)
	}
}
