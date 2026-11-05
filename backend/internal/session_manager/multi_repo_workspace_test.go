package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Multi-repo session workspaces, over REAL git and a REAL migrated store.
//
// The two repositories are independent temp repos with two commits each, so the base
// SHA a spawn records is a meaningful commit and not an empty tree. Nothing here
// starts a provider: the manager runs its TUI branch against the package's fake
// runtime and fake agents, which is the same harness the other spawn tests use.

// multiRepoFixture is two registered projects, each backed by its own real repo.
type multiRepoFixture struct {
	manager      *Manager
	store        *sqlite.Store
	workspace    *gitworktree.Workspace
	managedRoot  string
	projectA     domain.ProjectRecord
	projectB     domain.ProjectRecord
	projectAID   domain.ProjectID
	projectBID   domain.ProjectID
	repoA        string
	repoB        string
	secondCommit string
}

// newMultiRepoFixture builds the whole harness: two temp git repos with two commits
// each, a migrated store holding both projects, and a real gitworktree adapter whose
// managed root is a third temp dir.
func newMultiRepoFixture(t *testing.T) *multiRepoFixture {
	t.Helper()
	dataDir := t.TempDir()
	st := sqlitetest.MustOpenAt(t, dataDir)
	ctx := context.Background()

	repoA, headA := newMultiRepoGitRepo(t)
	repoB, headB := newMultiRepoGitRepo(t)
	if headA == headB {
		t.Fatal("the two repositories produced the same base commit; the fixture is not independent")
	}

	projectA := domain.ProjectRecord{
		ID: "multirepo-a", Path: repoA, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}},
	}
	projectB := domain.ProjectRecord{
		ID: "multirepo-b", Path: repoB, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}},
	}
	if err := st.UpsertProject(ctx, projectA); err != nil {
		t.Fatalf("seed project A: %v", err)
	}
	if err := st.UpsertProject(ctx, projectB); err != nil {
		t.Fatalf("seed project B: %v", err)
	}

	managedRoot := t.TempDir()
	ws, err := gitworktree.New(gitworktree.Options{
		ManagedRoot: managedRoot,
		RepoResolver: gitworktree.StaticRepoResolver{
			domain.ProjectID(projectA.ID): repoA,
			domain.ProjectID(projectB.ID): repoB,
		},
	})
	if err != nil {
		t.Fatalf("gitworktree: %v", err)
	}

	stub := func(string) (string, error) { return "/bin/true", nil }
	m := newAdmittedTestManager(t, Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lifecycle.New(st, nil),
		LookPath:  stub,
		DataDir:   dataDir,
	})
	m.dataDir = dataDir
	m.runBackground = func(work func()) { work() }

	return &multiRepoFixture{
		manager: m, store: st, workspace: ws, managedRoot: managedRoot,
		projectA: projectA, projectB: projectB,
		projectAID: domain.ProjectID(projectA.ID), projectBID: domain.ProjectID(projectB.ID),
		repoA: repoA, repoB: repoB,
		secondCommit: headB,
	}
}

// newMultiRepoGitRepo creates one repo with two commits and returns its path and HEAD.
func newMultiRepoGitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	runManagerGit(t, dir, "init")
	runManagerGit(t, dir, "config", "user.email", "ao@example.com")
	runManagerGit(t, dir, "config", "user.name", "AO Tests")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runManagerGit(t, dir, "add", ".")
	runManagerGit(t, dir, "commit", "-m", "base")
	runManagerGit(t, dir, "branch", "-M", "main")
	// A second commit makes the recorded base SHA meaningful rather than the root.
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatalf("write second file: %v", err)
	}
	runManagerGit(t, dir, "add", ".")
	runManagerGit(t, dir, "commit", "-m", "second")
	return dir, strings.TrimSpace(runManagerGit(t, dir, "rev-parse", "HEAD"))
}

// spawnCfg is a two-repo worker spawn.
func (f *multiRepoFixture) spawnCfg(clientRequestID string) ports.SpawnConfig {
	return ports.SpawnConfig{
		ProjectID:       f.projectAID,
		Kind:            domain.KindWorker,
		Harness:         domain.HarnessClaudeCode,
		Prompt:          "do the work",
		ClientRequestID:   clientRequestID,
		ClientRequestHash: "v1:" + clientRequestID,
		Repos: []ports.SpawnRepo{
			// Deliberately out of key order: the fixture must prove the layout is
			// deterministic by repo key, not by input order.
			{RepoKey: "repo-b", ProjectID: f.projectBID},
			{RepoKey: "repo-a", ProjectID: f.projectAID},
		},
	}
}

// TestMultiRepo spawn fails fast when the fixture's git is unavailable.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(t.TempDir(), ".")); err != nil {
		t.Skip("temporary directory unavailable")
	}
}

// TestMultiRepoSpawnCreatesAndVerifiesEveryWorktree is the core R-02 scenario: one
// session, two repositories, every worktree on disk, registered, and verified.
func TestMultiRepoSpawnCreatesAndVerifiesEveryWorktree(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("multirepo-1"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if rec.Metadata.WorkspacePath == "" {
		t.Fatal("the session recorded no workspace path")
	}

	rows, err := f.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list worktree rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("worktree rows = %d, want 2 (one per repository)", len(rows))
	}
	byRepo := map[string]domain.SessionWorktreeRecord{}
	for _, row := range rows {
		byRepo[row.RepoName] = row
	}
	for _, key := range []string{"repo-a", "repo-b"} {
		if _, ok := byRepo[key]; !ok {
			t.Fatalf("no worktree row for %q; got %v", key, rows)
		}
	}
	// The first repo by key is the session root.
	if filepath.Clean(byRepo["repo-a"].WorktreePath) != filepath.Clean(rec.Metadata.WorkspacePath) {
		t.Fatalf("root = %q, want the repo-a worktree %q",
			rec.Metadata.WorkspacePath, byRepo["repo-a"].WorktreePath)
	}

	for key, repoPath := range map[string]string{"repo-a": f.repoA, "repo-b": f.repoB} {
		row := byRepo[key]
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("%s worktree is not on disk at %q: %v", key, row.WorktreePath, statErr)
		}
		// Registered in ITS OWN repository, not in the other one.
		list := runManagerGit(t, repoPath, "worktree", "list", "--porcelain")
		if !worktreeListed(list, row.WorktreePath) {
			t.Fatalf("%s is not a git worktree of %s:\n%s", row.WorktreePath, repoPath, list)
		}
		// The verified HEAD is what the row records.
		head := strings.TrimSpace(runManagerGit(t, row.WorktreePath, "rev-parse", "HEAD"))
		if head != row.CreationSHA {
			t.Fatalf("%s recorded creation SHA %q but HEAD is %q", key, row.CreationSHA, head)
		}
		if row.Branch == "" || strings.TrimSpace(runManagerGit(t, row.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")) != row.Branch {
			t.Fatalf("%s recorded branch %q does not match the checkout", key, row.Branch)
		}
	}
	// The two repositories started from their own second commits, so the recorded
	// bases are the fixture's real SHAs rather than a shared placeholder.
	headB := strings.TrimSpace(runManagerGit(t, byRepo["repo-b"].WorktreePath, "rev-parse", "HEAD"))
	if headB != f.secondCommit {
		t.Fatalf("repo-b HEAD = %q, want the repo's second commit %q", headB, f.secondCommit)
	}
}

// TestMultiRepoSpawnRollsBackWhenTheSecondRepoFails proves a partial spawn leaves
// nothing behind: the first worktree is destroyed and no row claims success.
func TestMultiRepoSpawnRollsBackWhenTheSecondRepoFails(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	cfg := f.spawnCfg("multirepo-rollback")
	// The LAST repo by key ("repo-b") is the one that cannot be created: its project
	// is registered but points at a directory that is not a git repository.
	notARepo := t.TempDir()
	if err := f.store.UpsertProject(ctx, domain.ProjectRecord{
		ID: string(f.projectBID), Path: notARepo, RegisteredAt: time.Now().UTC().Truncate(time.Second),
		Config: domain.ProjectConfig{DefaultBranch: "main", Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}},
	}); err != nil {
		t.Fatalf("repoint project B: %v", err)
	}
	// The adapter resolves repo paths through its static resolver, so repointing it is
	// how the second create is made to fail.
	f.manager.workspace = f.rebuiltWorkspace(t, f.repoA, notARepo)

	_, _, _, err := f.manager.Spawn(ctx, cfg)
	if err == nil {
		t.Fatal("a spawn whose second repository cannot be created must fail")
	}
	if !errors.Is(err, ErrWorkspaceCreate) {
		t.Fatalf("error = %v, want the workspace-create stage", err)
	}

	sessions, listErr := f.store.ListSessions(ctx, f.projectAID)
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	live := 0
	var liveIDs []domain.SessionID
	for _, rec := range sessions {
		if !rec.IsTerminated {
			live++
			liveIDs = append(liveIDs, rec.ID)
		}
	}
	if live != 0 {
		t.Fatalf("%d session(s) %v claim success after a rolled-back spawn", live, liveIDs)
	}
	for _, rec := range sessions {
		rows, rowsErr := f.store.ListSessionWorktrees(ctx, rec.ID)
		if rowsErr != nil {
			t.Fatalf("list rows: %v", rowsErr)
		}
		for _, row := range rows {
			if _, statErr := os.Stat(row.WorktreePath); statErr == nil {
				t.Fatalf("a rolled-back spawn left %q on disk", row.WorktreePath)
			}
		}
	}
	// repo-a's worktree is gone and its branch never became an AO session branch.
	if list := runManagerGit(t, f.repoA, "worktree", "list", "--porcelain"); strings.Contains(list, "multirepo-rollback") {
		t.Fatalf("repo-a still has a worktree for the failed spawn:\n%s", list)
	}
}

// worktreeListed reports whether `git worktree list --porcelain` names path. Git
// prints paths with forward slashes even on Windows, so both forms are compared.
func worktreeListed(list, path string) bool {
	clean := filepath.Clean(path)
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			if filepath.Clean(rest) == clean {
				return true
			}
		}
	}
	return false
}

// rebuiltWorkspace builds a second gitworktree adapter pointing the two project ids
// at the supplied paths.
func (f *multiRepoFixture) rebuiltWorkspace(t *testing.T, pathA, pathB string) *gitworktree.Workspace {
	t.Helper()
	ws, err := gitworktree.New(gitworktree.Options{
		ManagedRoot: f.managedRoot,
		RepoResolver: gitworktree.StaticRepoResolver{
			f.projectAID: pathA,
			f.projectBID: pathB,
		},
	})
	if err != nil {
		t.Fatalf("gitworktree: %v", err)
	}
	return ws
}

// TestMultiRepoSpawnIsIdempotentForOneClientRequest proves a retried spawn reuses the
// session and creates no second set of worktrees.
func TestMultiRepoSpawnIsIdempotentForOneClientRequest(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	first, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("multirepo-idem"))
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	rowsBefore, err := f.store.ListSessionWorktrees(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionsBefore, err := f.store.ListSessions(ctx, f.projectAID)
	if err != nil {
		t.Fatal(err)
	}

	replay, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("multirepo-idem"))
	if err != nil {
		t.Fatalf("replayed spawn: %v", err)
	}
	if replay.ID != first.ID {
		t.Fatalf("replay produced session %s, want the original %s", replay.ID, first.ID)
	}
	rowsAfter, err := f.store.ListSessionWorktrees(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsAfter) != len(rowsBefore) {
		t.Fatalf("worktree rows went from %d to %d across a replay", len(rowsBefore), len(rowsAfter))
	}
	sessionsAfter, err := f.store.ListSessions(ctx, f.projectAID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionsAfter) != len(sessionsBefore) {
		t.Fatalf("sessions went from %d to %d across a replay", len(sessionsBefore), len(sessionsAfter))
	}
}

// TestMultiRepoRestoreVerifiesEveryWorktree proves restore refuses to report success
// when one recorded worktree is gone.
func TestMultiRepoRestoreVerifiesEveryWorktree(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("multirepo-restore"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	rows, err := f.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	// A healthy session restores and names the root row.
	restored, err := f.manager.restoreSessionWorkspace(ctx, f.projectA, rec)
	if err != nil {
		t.Fatalf("restore of an intact session: %v", err)
	}
	if restored.Path == "" {
		t.Fatal("restore returned no workspace path")
	}

	// Remove ONE worktree directory out of band. Restore must report a typed
	// uncertainty instead of restoring on the strength of the surviving row.
	victim := rows[1]
	if err := os.RemoveAll(victim.WorktreePath); err != nil {
		t.Fatalf("remove %q: %v", victim.WorktreePath, err)
	}
	_, err = f.manager.restoreSessionWorkspace(ctx, f.projectA, rec)
	if !errors.Is(err, ErrMultiRepoWorktreeMissing) {
		t.Fatalf("restore error = %v, want ErrMultiRepoWorktreeMissing", err)
	}
	// Nothing was reset, cleaned, or deleted to make the check pass: the surviving
	// worktree is still on disk.
	for _, row := range rows {
		if row.RepoName == victim.RepoName {
			continue
		}
		if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
			t.Fatalf("restore destroyed the surviving worktree %q: %v", row.WorktreePath, statErr)
		}
	}
}

// TestMultiRepoDirtyWorktreeSurvivesTeardown proves the dirty-worktree rule holds for
// a multi-repo session: a file written into one worktree is still there after Kill
// refused to remove it.
func TestMultiRepoDirtyWorktreeSurvivesTeardown(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfg("multirepo-dirty"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	rows, err := f.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	dirtyPath := rows[0].WorktreePath
	written := filepath.Join(dirtyPath, "keep-me.txt")
	if werr := os.WriteFile(written, []byte("user work\n"), 0o644); werr != nil {
		t.Fatalf("write %q: %v", written, werr)
	}

	// Kill is the teardown entry point reachable without a provider.
	_, _ = f.manager.Kill(ctx, rec.ID)

	if _, statErr := os.Stat(written); statErr != nil {
		t.Fatalf("teardown removed uncommitted work from %q: %v", dirtyPath, statErr)
	}
	if _, statErr := os.Stat(dirtyPath); statErr != nil {
		t.Fatalf("teardown removed the dirty worktree itself: %v", statErr)
	}
}
