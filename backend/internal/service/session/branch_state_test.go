package session

import (
	"context"
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

func TestReconcileSessionBranchStateTracksCommitsAndPushes(t *testing.T) {
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "checkout", "-b", "feat/x")
	commit := func(name string) {
		writeWorkspaceFile(t, repo, name, "package main\n")
		runGit(t, repo, "add", name)
		runGit(t, repo, "commit", "-m", "add "+name)
	}
	commit("a.go")
	commit("b.go")

	st := &branchStateStore{fakeStore: newFakeStore()}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{
		WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "origin/main",
	}}
	svc := &Service{store: st}
	reconcile := func(want domain.SessionBranchState) {
		t.Helper()
		if err := svc.ReconcileSessionBranchState(context.Background(), "ao-1"); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if got := st.sessions["ao-1"].BranchState; got == nil || *got != want {
			t.Fatalf("branch state = %+v, want %+v", got, want)
		}
	}

	// Tracking origin/main is not a push: the branch itself is not on the remote.
	runGit(t, repo, "branch", "--set-upstream-to", "origin/main")
	reconcile(domain.SessionBranchState{Commits: 2, Unpushed: 2})

	runGit(t, repo, "push", "-u", "origin", "feat/x")
	reconcile(domain.SessionBranchState{Commits: 2, RemoteBranch: "origin/feat/x", Unpushed: 0})

	commit("c.go")
	reconcile(domain.SessionBranchState{Commits: 3, RemoteBranch: "origin/feat/x", Unpushed: 1})

	writes := st.writes
	reconcile(domain.SessionBranchState{Commits: 3, RemoteBranch: "origin/feat/x", Unpushed: 1})
	if st.writes != writes {
		t.Fatalf("unchanged facts were rewritten (%d writes, want %d)", st.writes, writes)
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
