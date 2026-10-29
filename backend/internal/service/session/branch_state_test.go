package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type branchStateStore struct {
	*fakeStore
	writes int
}

func (s *branchStateStore) SetSessionBranchState(_ context.Context, id domain.SessionID, state domain.SessionBranchState) (bool, error) {
	rec := s.sessions[id]
	rec.BranchState = &state
	s.sessions[id] = rec
	s.writes++
	return true, nil
}

// newPushableRepo returns a repo whose main is pushed to a bare origin.
func newPushableRepo(t *testing.T) string {
	t.Helper()
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	return repo
}

func commitFile(t *testing.T, repo, name string) {
	t.Helper()
	writeWorkspaceFile(t, repo, name, "package main\n")
	runGit(t, repo, "add", name)
	runGit(t, repo, "commit", "-m", "add "+name)
}

// headed sets want's Head to dir's checked-out commit.
func headed(t *testing.T, dir string, want domain.SessionBranchState) domain.SessionBranchState {
	t.Helper()
	want.Head = strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	return want
}

func TestReconcileSessionBranchStateTracksCommitsAndPushes(t *testing.T) {
	repo := newPushableRepo(t)
	runGit(t, repo, "checkout", "-b", "feat/x")
	commitFile(t, repo, "a.go")
	commitFile(t, repo, "b.go")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseRef: "refs/remotes/origin/main",
	}}
	svc := &Service{store: st}
	reconcile := func(want domain.SessionBranchState) {
		t.Helper()
		if err := svc.ReconcileSessionBranchState(context.Background(), "ao-1"); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		want = headed(t, repo, want)
		if got := st.sessions["ao-1"].BranchState; got == nil || *got != want {
			t.Fatalf("branch state = %+v, want %+v", got, want)
		}
	}

	// Tracking origin/main is not a push: the branch itself is not on the remote.
	runGit(t, repo, "branch", "--set-upstream-to", "origin/main")
	reconcile(domain.SessionBranchState{Commits: 2, Unpushed: 2, Base: "main", BaseRef: "origin/main"})

	runGit(t, repo, "push", "-u", "origin", "feat/x")
	reconcile(domain.SessionBranchState{Commits: 2, RemoteBranch: "origin/feat/x", Unpushed: 0, Base: "main", BaseRef: "origin/main"})

	commitFile(t, repo, "c.go")
	reconcile(domain.SessionBranchState{Commits: 3, RemoteBranch: "origin/feat/x", Unpushed: 1, Base: "main", BaseRef: "origin/main"})

	writes := st.writes
	reconcile(domain.SessionBranchState{Commits: 3, RemoteBranch: "origin/feat/x", Unpushed: 1, Base: "main", BaseRef: "origin/main"})
	if st.writes != writes {
		t.Fatalf("unchanged facts were rewritten (%d writes, want %d)", st.writes, writes)
	}
}

func TestReconcileBranchStatesFollowsTheWorktreeBranch(t *testing.T) {
	repo := newPushableRepo(t)
	worktree := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-b", "ao/s1/root", worktree)
	// The agent moved to its own branch; the recorded branch is stale.
	runGit(t, worktree, "checkout", "-b", "ao/s1/feature")
	commitFile(t, worktree, "a.go")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["s1"] = domain.SessionRecord{ID: "s1", Metadata: domain.SessionMetadata{
		Branch: "ao/s1/root", WorkspacePath: worktree, DiffBaseRef: "refs/remotes/origin/main",
	}}
	if err := (&Service{store: st}).ReconcileBranchStates(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	want := headed(t, worktree, domain.SessionBranchState{Commits: 1, Unpushed: 1, Base: "main", BaseRef: "origin/main"})
	if got := st.sessions["s1"].BranchState; got == nil || *got != want {
		t.Fatalf("branch state = %+v, want %+v", got, want)
	}
}

func TestReconcileBranchStatesRunsGitOnlyWhenTipsMove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("counts git through a shell wrapper")
	}
	repo := newPushableRepo(t)
	st := &branchStateStore{fakeStore: newFakeStore()}
	worktrees := map[domain.SessionID]string{}
	for _, id := range []domain.SessionID{"s1", "s2"} {
		worktree := filepath.Join(t.TempDir(), string(id))
		runGit(t, repo, "worktree", "add", "-b", "ao/"+string(id), worktree)
		commitFile(t, worktree, string(id)+".go")
		worktrees[id] = worktree
		st.sessions[id] = domain.SessionRecord{ID: id, Metadata: domain.SessionMetadata{
			WorkspacePath: worktree, DiffBaseRef: "refs/remotes/origin/main",
		}}
	}
	svc := &Service{store: st}
	gitCalls := countGitCalls(t)
	pass := func() int {
		t.Helper()
		before := gitCalls()
		if err := svc.ReconcileBranchStates(context.Background()); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		return gitCalls() - before
	}

	// One for-each-ref for the shared repository, then one commit count per
	// session; neither branch is on the remote, so no unpushed count.
	if got := pass(); got != 3 {
		t.Fatalf("first pass ran %d git processes, want 3", got)
	}
	if got := pass(); got != 1 {
		t.Fatalf("idle pass ran %d git processes, want only the for-each-ref", got)
	}
	commitFile(t, worktrees["s1"], "more.go")
	if got := pass(); got != 2 {
		t.Fatalf("pass after one commit ran %d git processes, want 2", got)
	}
	want := headed(t, worktrees["s1"], domain.SessionBranchState{Commits: 2, Unpushed: 2, Base: "main", BaseRef: "origin/main"})
	if got := st.sessions["s1"].BranchState; got == nil || *got != want {
		t.Fatalf("s1 branch state = %+v, want %+v", got, want)
	}
}

func reconcileOne(t *testing.T, st *branchStateStore, id domain.SessionID) *domain.SessionBranchState {
	t.Helper()
	if err := (&Service{store: st}).ReconcileSessionBranchState(context.Background(), id); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return st.sessions[id].BranchState
}

func TestReconcileSessionBranchStateCountsHowFarBehindTheBase(t *testing.T) {
	repo := newPushableRepo(t)
	runGit(t, repo, "checkout", "-b", "feat/x")
	commitFile(t, repo, "a.go")
	// The base moves on after the branch split: two commits land on main.
	runGit(t, repo, "checkout", "main")
	commitFile(t, repo, "m1.go")
	commitFile(t, repo, "m2.go")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "feat/x")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseRef: "refs/remotes/origin/main",
	}}
	want := headed(t, repo, domain.SessionBranchState{Commits: 1, Unpushed: 1, Base: "main", BaseRef: "origin/main", Behind: 2})
	if got := reconcileOne(t, st, "ao-1"); got == nil || *got != want {
		t.Fatalf("branch state = %+v, want %+v", got, want)
	}
}

func TestReconcileSessionBranchStateReportsConflictsWithTheBase(t *testing.T) {
	repo := newPushableRepo(t)
	writeWorkspaceFile(t, repo, "shared.go", "package main\n")
	runGit(t, repo, "add", "shared.go")
	runGit(t, repo, "commit", "-m", "add shared")
	runGit(t, repo, "push", "origin", "main")
	edit := func(content string) {
		t.Helper()
		writeWorkspaceFile(t, repo, "shared.go", content)
		runGit(t, repo, "commit", "-am", "edit shared")
	}
	runGit(t, repo, "checkout", "-b", "feat/x")
	edit("package main\n\nconst side = \"branch\"\n")
	// main edits the same lines after the split.
	runGit(t, repo, "checkout", "main")
	edit("package main\n\nconst side = \"main\"\n")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "checkout", "feat/x")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseRef: "refs/remotes/origin/main",
	}}
	want := headed(t, repo, domain.SessionBranchState{Commits: 1, Unpushed: 1, Base: "main", BaseRef: "origin/main", Behind: 1, Conflicts: true})
	if got := reconcileOne(t, st, "ao-1"); got == nil || *got != want {
		t.Fatalf("branch state = %+v, want %+v", got, want)
	}
}

func TestReconcileSessionBranchStateLabelsALocalBase(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "checkout", "-b", "feat/x")
	commitFile(t, repo, "a.go")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseRef: "refs/heads/main",
	}}
	want := headed(t, repo, domain.SessionBranchState{Commits: 1, Unpushed: 1, Base: "main", BaseRef: "main"})
	if got := reconcileOne(t, st, "ao-1"); got == nil || *got != want {
		t.Fatalf("branch state = %+v, want %+v", got, want)
	}
}

// A base known only as a recorded commit has no branch to be behind, so the
// state must not claim "0 behind".
func TestReconcileSessionBranchStateLeavesBaseUnsetForARecordedCommit(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	baseSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-b", "feat/x")
	commitFile(t, repo, "a.go")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseSHA: baseSHA,
	}}
	want := headed(t, repo, domain.SessionBranchState{Commits: 1, Unpushed: 1})
	if got := reconcileOne(t, st, "ao-1"); got == nil || *got != want {
		t.Fatalf("branch state = %+v, want %+v", got, want)
	}
}

func TestBranchBaseNames(t *testing.T) {
	for ref, want := range map[string][2]string{
		"refs/remotes/origin/main":          {"main", "origin/main"},
		"refs/remotes/upstream/release/1.2": {"release/1.2", "upstream/release/1.2"},
		"refs/heads/main":                   {"main", "main"},
		"refs/heads/release/1.2":            {"release/1.2", "release/1.2"},
	} {
		name, display := branchBaseNames(ref)
		if name != want[0] || display != want[1] {
			t.Errorf("branchBaseNames(%q) = %q, %q; want %q, %q", ref, name, display, want[0], want[1])
		}
	}
}

func TestReconcileSessionBranchStateSkipsTerminatedSessions(t *testing.T) {
	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir()}}
	if err := (&Service{store: st}).ReconcileSessionBranchState(context.Background(), "ao-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if st.writes != 0 {
		t.Fatalf("terminated session got %d branch writes", st.writes)
	}
}

// countGitCalls puts a logging git first on PATH and returns a counter of the
// git processes started since.
func countGitCalls(t *testing.T) func() int {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho >> '" + log + "'\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		raw, _ := os.ReadFile(log)
		return strings.Count(string(raw), "\n")
	}
}
