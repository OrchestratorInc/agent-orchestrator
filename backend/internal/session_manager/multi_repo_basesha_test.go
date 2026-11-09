package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Pinned base commits (ports.SpawnRepo.BaseSHA -> ports.WorkspaceConfig.BaseSHA).
//
// The defect these cover: a spawn that pinned a commit used to seed each new
// session branch from the repository's DEFAULT branch tip, because the pin never
// reached the workspace adapter. Every test here therefore asserts against a REAL
// `git rev-parse HEAD` read inside the created worktree, not against any value
// this code passed back to itself.

// spawnCfgPinned is spawnCfg with an explicit per-repo pinned base commit.
func (f *multiRepoFixture) spawnCfgPinned(clientRequestID, baseSHAA, baseSHAB string) ports.SpawnConfig {
	cfg := f.spawnCfg(clientRequestID)
	for i := range cfg.Repos {
		switch cfg.Repos[i].RepoKey {
		case "repo-a":
			cfg.Repos[i].BaseSHA = baseSHAA
		case "repo-b":
			cfg.Repos[i].BaseSHA = baseSHAB
		}
	}
	return cfg
}

// worktreeHeadOf reads the commit a created worktree actually has checked out.
func worktreeHeadOf(t *testing.T, path string) string {
	t.Helper()
	return strings.TrimSpace(runManagerGit(t, path, "rev-parse", "HEAD"))
}

// rowsByRepo indexes a session's recorded worktree rows by repo key.
func rowsByRepo(t *testing.T, f *multiRepoFixture, sessionID domain.SessionID) map[string]domain.SessionWorktreeRecord {
	t.Helper()
	rows, err := f.store.ListSessionWorktrees(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list worktree rows: %v", err)
	}
	out := make(map[string]domain.SessionWorktreeRecord, len(rows))
	for _, row := range rows {
		out[row.RepoName] = row
	}
	return out
}

// TestMultiRepoSpawnSeedsFromPinnedHistoricalSHA is the defect itself: a pin one
// commit behind the tip must produce a worktree AT that commit, and the row must
// record it as both BaseSHA and CreationSHA.
func TestMultiRepoSpawnSeedsFromPinnedHistoricalSHA(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	historical := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	tip := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD"))
	if historical == tip {
		t.Fatal("fixture produced no history to pin: HEAD~1 == HEAD")
	}

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-historical", historical, ""))
	if err != nil {
		t.Fatalf("spawn with a pinned base: %v", err)
	}
	rows := rowsByRepo(t, f, rec.ID)
	row, ok := rows["repo-a"]
	if !ok {
		t.Fatalf("no repo-a row; rows=%v", rows)
	}

	// The filesystem fact, not the recorded value.
	if head := worktreeHeadOf(t, row.WorktreePath); head != historical {
		t.Fatalf("repo-a worktree HEAD = %s, want the pinned commit %s (repository tip is %s)", head, historical, tip)
	}
	if row.CreationSHA != historical {
		t.Fatalf("repo-a CreationSHA = %q, want the pinned commit %q", row.CreationSHA, historical)
	}
	if row.BaseSHA != historical {
		t.Fatalf("repo-a BaseSHA = %q, want the pinned commit %q", row.BaseSHA, historical)
	}
	// The unpinned repo keeps the old default-branch behaviour.
	if head := worktreeHeadOf(t, rows["repo-b"].WorktreePath); head != f.secondCommit {
		t.Fatalf("repo-b (unpinned) HEAD = %s, want its default tip %s", head, f.secondCommit)
	}
}

// TestMultiRepoSpawnPinnedSHASurvivesDefaultBranchAdvancing proves the pin is
// honoured after the source repository moved on: the SHA is computed, another
// commit lands on main, and the worktree must still start at the older commit.
func TestMultiRepoSpawnPinnedSHASurvivesDefaultBranchAdvancing(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinned := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD"))
	// main advances AFTER the caller planned from `pinned`.
	if err := os.WriteFile(filepath.Join(f.repoA, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatalf("write later file: %v", err)
	}
	runManagerGit(t, f.repoA, "add", ".")
	runManagerGit(t, f.repoA, "commit", "-m", "advance main after planning")
	advanced := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD"))
	if advanced == pinned {
		t.Fatal("the source repository did not advance")
	}

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-advanced", pinned, ""))
	if err != nil {
		t.Fatalf("spawn with a pinned base: %v", err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	if head := worktreeHeadOf(t, row.WorktreePath); head != pinned {
		t.Fatalf("repo-a worktree HEAD = %s, want the pinned commit %s (main advanced to %s)", head, pinned, advanced)
	}
	if head := worktreeHeadOf(t, rowsByRepo(t, f, rec.ID)["repo-a"].WorktreePath); head == advanced {
		t.Fatalf("the worktree followed the advanced default branch to %s instead of the pin", advanced)
	}
}

// TestMultiRepoSpawnPinsEachRepoIndependently pins two DIFFERENT historical
// commits — repo-a at HEAD~1, repo-b at its root commit — so a pin that leaked
// between repositories would be visible.
func TestMultiRepoSpawnPinsEachRepoIndependently(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	// Give repo-b a third commit so its HEAD~1 is a genuinely different commit
	// from repo-a's HEAD~1 (the two roots are already different objects).
	if err := os.WriteFile(filepath.Join(f.repoB, "third.txt"), []byte("third\n"), 0o644); err != nil {
		t.Fatalf("write third file: %v", err)
	}
	runManagerGit(t, f.repoB, "add", ".")
	runManagerGit(t, f.repoB, "commit", "-m", "third")

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(runManagerGit(t, f.repoB, "rev-parse", "HEAD~2"))
	if pinA == pinB {
		t.Fatal("the fixture produced the same pinned commit for both repositories")
	}

	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-pair", pinA, pinB))
	if err != nil {
		t.Fatalf("spawn with two pinned bases: %v", err)
	}
	rows := rowsByRepo(t, f, rec.ID)
	for _, tc := range []struct {
		key  string
		want string
	}{{"repo-a", pinA}, {"repo-b", pinB}} {
		row, ok := rows[tc.key]
		if !ok {
			t.Fatalf("no %s row", tc.key)
		}
		if head := worktreeHeadOf(t, row.WorktreePath); head != tc.want {
			t.Fatalf("%s worktree HEAD = %s, want its own pin %s", tc.key, head, tc.want)
		}
		if row.CreationSHA != tc.want || row.BaseSHA != tc.want {
			t.Fatalf("%s recorded base=%q creation=%q, want %q for both",
				tc.key, row.BaseSHA, row.CreationSHA, tc.want)
		}
	}
}

// TestMultiRepoSpawnRefusesUnknownPinnedSHA proves a pin that does not exist is
// refused with the typed error and costs nothing: no row, no directory, no
// registration.
func TestMultiRepoSpawnRefusesUnknownPinnedSHA(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	unknown := strings.Repeat("0", 40)
	_, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-unknown", unknown, ""))
	if err == nil {
		t.Fatal("a spawn pinned to an unknown commit must fail")
	}
	if !errors.Is(err, ports.ErrWorkspaceBaseSHAUnavailable) {
		t.Fatalf("error = %v, want ports.ErrWorkspaceBaseSHAUnavailable", err)
	}

	sessions, listErr := f.store.ListSessions(ctx, f.projectAID)
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	for _, rec := range sessions {
		rows, rowsErr := f.store.ListSessionWorktrees(ctx, rec.ID)
		if rowsErr != nil {
			t.Fatalf("list rows: %v", rowsErr)
		}
		if len(rows) != 0 {
			t.Fatalf("a refused pinned spawn left %d worktree row(s) for %s", len(rows), rec.ID)
		}
	}
	// Nothing was registered in either repository, and nothing was created under
	// the managed root. Git prints paths with forward slashes even on Windows,
	// so registration is matched through worktreeListed (normalized).
	for _, repo := range []string{f.repoA, f.repoB} {
		list := runManagerGit(t, repo, "worktree", "list", "--porcelain")
		if !worktreeListed(list, repo) {
			t.Fatalf("worktree list for %s is missing its own checkout:\n%s", repo, list)
		}
		for _, line := range strings.Split(list, "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
			if ok && strings.Contains(rest, "multirepo-pinned-unknown") {
				t.Fatalf("a refused pinned spawn registered a worktree in %s:\n%s", repo, list)
			}
		}
	}
	if entries, readErr := os.ReadDir(f.managedRoot); readErr == nil && len(entries) != 0 {
		t.Fatalf("a refused pinned spawn left %d entries under the managed root", len(entries))
	}
}

// TestMultiRepoSpawnRefusesMalformedPinnedSHA covers the inputs that are not
// commit ids at all. Each must be refused as unavailable — never resolved as a
// ref expression, and never silently treated as "no pin".
func TestMultiRepoSpawnRefusesMalformedPinnedSHA(t *testing.T) {
	requireGit(t)
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"not a sha", "not-a-sha"},
		{"ref expression", "HEAD~1"},
		{"whitespace only", "   "},
		{"thirty nine characters", strings.Repeat("a", 39)},
		{"abbreviated", strings.Repeat("a", 7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMultiRepoFixture(t)
			ctx := context.Background()

			_, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-malformed", tc.value, ""))
			if err == nil {
				t.Fatalf("spawn pinned to %q must fail", tc.value)
			}
			if !errors.Is(err, ports.ErrWorkspaceBaseSHAUnavailable) {
				t.Fatalf("error = %v, want ports.ErrWorkspaceBaseSHAUnavailable", err)
			}

			sessions, listErr := f.store.ListSessions(ctx, f.projectAID)
			if listErr != nil {
				t.Fatalf("list sessions: %v", listErr)
			}
			for _, rec := range sessions {
				rows, rowsErr := f.store.ListSessionWorktrees(ctx, rec.ID)
				if rowsErr != nil {
					t.Fatalf("list rows: %v", rowsErr)
				}
				if len(rows) != 0 {
					t.Fatalf("refused pin %q left %d worktree row(s)", tc.value, len(rows))
				}
			}
		})
	}
}

// TestMultiRepoSpawnRollsBackPinnedFirstRepoWhenSecondFails proves rollback
// correctness for a pinned spawn: repo-a is created from a pin, repo-b's pin is
// invalid, so repo-a's worktree must be gone and unregistered afterwards.
func TestMultiRepoSpawnRollsBackPinnedFirstRepoWhenSecondFails(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	_, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-rollback", pinA, strings.Repeat("0", 40)))
	if err == nil {
		t.Fatal("a spawn whose second repository cannot be pinned must fail")
	}
	if !errors.Is(err, ports.ErrWorkspaceBaseSHAUnavailable) {
		t.Fatalf("error = %v, want ports.ErrWorkspaceBaseSHAUnavailable", err)
	}

	sessions, listErr := f.store.ListSessions(ctx, f.projectAID)
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	for _, rec := range sessions {
		rows, rowsErr := f.store.ListSessionWorktrees(ctx, rec.ID)
		if rowsErr != nil {
			t.Fatalf("list rows: %v", rowsErr)
		}
		if len(rows) != 0 {
			t.Fatalf("rollback left %d worktree row(s) for %s: %v", len(rows), rec.ID, rows)
		}
		for _, row := range rows {
			if _, statErr := os.Stat(row.WorktreePath); statErr == nil {
				t.Fatalf("rollback left %q on disk", row.WorktreePath)
			}
		}
	}
	// The pinned first repo's worktree is gone from disk AND from git's list.
	if list := runManagerGit(t, f.repoA, "worktree", "list", "--porcelain"); strings.Contains(list, "multirepo-pinned-rollback") {
		t.Fatalf("repo-a still registers a worktree for the rolled-back spawn:\n%s", list)
	}
}

// TestMultiRepoPinnedDirtyWorktreeSurvivesTeardown is the pinned variant of the
// dirty-worktree rule, over BOTH paths that could destroy work: a spawn retry
// (the idempotent replay of the same request) and Kill's teardown.
func TestMultiRepoPinnedDirtyWorktreeSurvivesTeardown(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	cfg := f.spawnCfgPinned("multirepo-pinned-dirty", pinA, "")
	rec, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]
	written := filepath.Join(row.WorktreePath, "keep-me.txt")
	if err := os.WriteFile(written, []byte("user work\n"), 0o644); err != nil {
		t.Fatalf("write %q: %v", written, err)
	}

	// A retry of the same spawn request must not recreate, reset, or replace the
	// worktree that now holds uncommitted work.
	if _, _, _, err := f.manager.Spawn(ctx, cfg); err != nil {
		t.Fatalf("retried pinned spawn: %v", err)
	}
	if data, readErr := os.ReadFile(written); readErr != nil || string(data) != "user work\n" {
		t.Fatalf("a spawn retry damaged uncommitted work: %q, %v", data, readErr)
	}
	if head := worktreeHeadOf(t, row.WorktreePath); head != pinA {
		t.Fatalf("a spawn retry moved the worktree from the pin %s to %s", pinA, head)
	}

	_, _ = f.manager.Kill(ctx, rec.ID)

	if _, statErr := os.Stat(written); statErr != nil {
		t.Fatalf("teardown removed uncommitted work from the pinned worktree: %v", statErr)
	}
	if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
		t.Fatalf("teardown removed the dirty pinned worktree itself: %v", statErr)
	}
}

// TestMultiRepoSpawnPinnedIsIdempotentForOneClientRequest re-runs the SAME pinned
// spawn: it must reuse the session, keep one worktree per repository, and still
// sit on the pin (the branch the first attempt created is accepted only because
// its tip equals the requested SHA).
func TestMultiRepoSpawnPinnedIsIdempotentForOneClientRequest(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(runManagerGit(t, f.repoB, "rev-parse", "HEAD~1"))
	cfg := f.spawnCfgPinned("multirepo-pinned-idem", pinA, pinB)

	first, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatalf("first pinned spawn: %v", err)
	}
	rowsBefore := rowsByRepo(t, f, first.ID)
	if len(rowsBefore) != 2 {
		t.Fatalf("worktree rows = %d, want 2", len(rowsBefore))
	}

	replay, _, _, err := f.manager.Spawn(ctx, cfg)
	if err != nil {
		t.Fatalf("replayed pinned spawn: %v", err)
	}
	if replay.ID != first.ID {
		t.Fatalf("replay produced session %s, want the original %s", replay.ID, first.ID)
	}
	rowsAfter := rowsByRepo(t, f, first.ID)
	if len(rowsAfter) != 2 {
		t.Fatalf("worktree rows after replay = %d, want 2", len(rowsAfter))
	}
	for _, tc := range []struct {
		key  string
		want string
	}{{"repo-a", pinA}, {"repo-b", pinB}} {
		if head := worktreeHeadOf(t, rowsAfter[tc.key].WorktreePath); head != tc.want {
			t.Fatalf("%s HEAD after replay = %s, want the pin %s", tc.key, head, tc.want)
		}
	}
}

// TestMultiRepoRestoreKeepsPerRepoProjectIDsAndPinnedSHAs is the restart case
// over a REOPENED store: after a restart every row must still name its own
// project (projectA for repo-a, projectB for repo-b, not the root session's
// project for both) and keep the pinned base and creation commits.
func TestMultiRepoRestoreKeepsPerRepoProjectIDsAndPinnedSHAs(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	pinB := strings.TrimSpace(runManagerGit(t, f.repoB, "rev-parse", "HEAD~1"))
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-pinned-restart", pinA, pinB))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	before := rowsByRepo(t, f, rec.ID)

	// A real restart: close the store and open the same data dir again.
	// sqlitetest.MustOpenAt cannot be used a second time on one data dir (it
	// clones a fresh template and refuses an existing ao.db), so this reopens
	// the durable file the way internal/cli/report_e2e_test.go does.
	dataDir := f.manager.dataDir
	if err := f.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	reopened, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("close reopened store: %v", closeErr)
		}
	})

	restoredManager := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    fakeAgents{},
		Workspace: f.workspace,
		Store:     reopened,
		Messenger: &fakeMessenger{},
		Lifecycle: reopenedLifecycle(t, reopened),
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
		DataDir:   dataDir,
	})
	restoredManager.dataDir = dataDir
	restoredManager.runBackground = func(work func()) { work() }

	projectA, ok, projErr := reopened.GetProject(ctx, string(f.projectAID))
	if projErr != nil || !ok {
		t.Fatalf("reload project A: ok=%v err=%v", ok, projErr)
	}
	restored, err := restoredManager.restoreSessionWorkspace(ctx, projectA, rec)
	if err != nil {
		t.Fatalf("restore after restart: %v", err)
	}
	if restored.Path == "" {
		t.Fatal("restore returned no workspace path")
	}

	after, err := reopened.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list rows after restore: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("worktree rows after restore = %d, want 2", len(after))
	}
	byRepo := map[string]domain.SessionWorktreeRecord{}
	for _, row := range after {
		byRepo[row.RepoName] = row
	}

	// ProjectID must be resolved from each repo's own path.
	infos := restoredManager.multiRepoRows(after)
	if len(infos) != 2 {
		t.Fatalf("repo infos = %d, want 2", len(infos))
	}
	projectByRepo := map[string]domain.ProjectID{}
	for _, info := range infos {
		projectByRepo[info.RepoName] = info.ProjectID
	}
	if projectByRepo["repo-a"] != f.projectAID {
		t.Fatalf("repo-a project = %q, want %q", projectByRepo["repo-a"], f.projectAID)
	}
	if projectByRepo["repo-b"] != f.projectBID {
		t.Fatalf("repo-b project = %q, want %q (the session root project is %q)",
			projectByRepo["repo-b"], f.projectBID, f.projectAID)
	}

	// The pinned commits survive the round trip unchanged.
	for _, tc := range []struct {
		key  string
		want string
	}{{"repo-a", pinA}, {"repo-b", pinB}} {
		row, ok := byRepo[tc.key]
		if !ok {
			t.Fatalf("no %s row after restore", tc.key)
		}
		if row.BaseSHA != tc.want || row.CreationSHA != tc.want {
			t.Fatalf("%s recorded base=%q creation=%q after restart, want %q for both (before: base=%q creation=%q)",
				tc.key, row.BaseSHA, row.CreationSHA, tc.want, before[tc.key].BaseSHA, before[tc.key].CreationSHA)
		}
		if head := worktreeHeadOf(t, row.WorktreePath); head != tc.want {
			t.Fatalf("%s HEAD after restart = %s, want the pin %s", tc.key, head, tc.want)
		}
	}
}

// TestMultiRepoRestoreRefusesWorktreeResetToUnrelatedHistory proves check (d):
// a worktree someone reset to a commit the recorded creation commit is not an
// ancestor of must be refused with a typed error, while nothing is reset,
// cleaned, or deleted to make the check pass.
func TestMultiRepoRestoreRefusesWorktreeResetToUnrelatedHistory(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-unrelated-history", pinA, ""))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]

	// An unrelated commit in the SAME repository: repo-a's root commit is not
	// reachable from it once the worktree's branch is reset onto it. Build it with
	// a root-less orphan branch so the two histories truly share no ancestor.
	runManagerGit(t, f.repoA, "checkout", "--orphan", "unrelated-base")
	if err := os.WriteFile(filepath.Join(f.repoA, "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}
	runManagerGit(t, f.repoA, "add", "unrelated.txt")
	runManagerGit(t, f.repoA, "commit", "-m", "unrelated history")
	unrelated := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD"))
	runManagerGit(t, f.repoA, "checkout", "main")

	// The worktree's branch is moved to that unrelated commit out of band.
	runManagerGit(t, row.WorktreePath, "reset", "--hard", unrelated)
	if head := worktreeHeadOf(t, row.WorktreePath); head != unrelated {
		t.Fatalf("worktree HEAD = %s, want the unrelated commit %s", head, unrelated)
	}

	projectA, ok, projErr := f.store.GetProject(ctx, string(f.projectAID))
	if projErr != nil || !ok {
		t.Fatalf("reload project A: ok=%v err=%v", ok, projErr)
	}
	_, err = f.manager.restoreSessionWorkspace(ctx, projectA, rec)
	if !errors.Is(err, ErrMultiRepoHistoryDiverged) {
		t.Fatalf("restore error = %v, want ErrMultiRepoHistoryDiverged", err)
	}

	// Nothing was touched: the worktree is still there, still at the unrelated
	// commit, and its uncommitted state is exactly what the reset left.
	if head := worktreeHeadOf(t, row.WorktreePath); head != unrelated {
		t.Fatalf("restore moved the worktree to %s, want it left at %s", head, unrelated)
	}
	if _, statErr := os.Stat(row.WorktreePath); statErr != nil {
		t.Fatalf("restore deleted the worktree it refused: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(row.WorktreePath, "unrelated.txt")); statErr != nil {
		t.Fatalf("restore removed the unrelated commit's file: %v", statErr)
	}
}

// TestMultiRepoRestoreAcceptsCommitsOnTopOfCreationSHA proves the other half of
// check (d): an agent that committed on top of its pinned branch must still
// restore, because the creation commit remains an ancestor of HEAD.
func TestMultiRepoRestoreAcceptsCommitsOnTopOfCreationSHA(t *testing.T) {
	requireGit(t)
	f := newMultiRepoFixture(t)
	ctx := context.Background()

	pinA := strings.TrimSpace(runManagerGit(t, f.repoA, "rev-parse", "HEAD~1"))
	rec, _, _, err := f.manager.Spawn(ctx, f.spawnCfgPinned("multirepo-committed-on-top", pinA, ""))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	row := rowsByRepo(t, f, rec.ID)["repo-a"]

	// The agent commits in its worktree, exactly as a real session would.
	if err := os.WriteFile(filepath.Join(row.WorktreePath, "agent.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
	runManagerGit(t, row.WorktreePath, "add", "agent.txt")
	runManagerGit(t, row.WorktreePath, "commit", "-m", "agent commit")
	agentHead := worktreeHeadOf(t, row.WorktreePath)
	if agentHead == pinA {
		t.Fatal("the agent commit did not move HEAD")
	}

	projectA, ok, projErr := f.store.GetProject(ctx, string(f.projectAID))
	if projErr != nil || !ok {
		t.Fatalf("reload project A: ok=%v err=%v", ok, projErr)
	}
	if _, err := f.manager.restoreSessionWorkspace(ctx, projectA, rec); err != nil {
		t.Fatalf("restore of a worktree with an agent commit on top: %v", err)
	}
	if head := worktreeHeadOf(t, row.WorktreePath); head != agentHead {
		t.Fatalf("restore moved the worktree from %s to %s", agentHead, head)
	}
}

// reopenedLifecycle builds the lifecycle recorder a manager over a freshly
// reopened store needs. It mirrors the fixture's construction.
func reopenedLifecycle(t *testing.T, store *sqlite.Store) *lifecycle.Manager {
	t.Helper()
	return lifecycle.New(store, nil)
}
