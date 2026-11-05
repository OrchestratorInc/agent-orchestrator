package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Pinned base commits at the adapter boundary.
//
// Every case runs against real git: the point of the pin is what git does with
// the seed, so nothing here may be asserted from the adapter's own bookkeeping.

// pinnedTestRepo builds a repo with three commits on main and returns the repo
// path plus the commit ids of HEAD and HEAD~1.
func pinnedTestRepo(t *testing.T, git string) (repo, head, previous string) {
	t.Helper()
	return pinnedTestRepoIn(t, git, t.TempDir())
}

func newPinnedWorkspace(t *testing.T, git, repo string) *Workspace {
	t.Helper()
	ws, err := New(Options{
		Binary:       git,
		ManagedRoot:  filepath.Join(t.TempDir(), "managed"),
		RepoResolver: StaticRepoResolver{"proj": repo},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return ws
}

// TestCreateSeedsFromPinnedBaseSHA is the adapter-level defect: the new branch
// starts at the pinned historical commit, not at the default branch tip.
func TestCreateSeedsFromPinnedBaseSHA(t *testing.T) {
	git := requireGit(t)
	repo, head, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if err != nil {
		t.Fatalf("create with a pinned base: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != previous {
		t.Fatalf("worktree HEAD = %s, want the pinned commit %s (tip is %s)", got, previous, head)
	}
	if info.BaseSHA != previous {
		t.Fatalf("info.BaseSHA = %q, want the pinned commit %q", info.BaseSHA, previous)
	}
}

// TestCreatePinnedBaseSHASurvivesAdvancedDefault proves the pin wins over the
// default branch: main moves on after the pin was chosen, and the worktree still
// starts where the caller asked.
func TestCreatePinnedBaseSHASurvivesAdvancedDefault(t *testing.T) {
	git := requireGit(t)
	repo, _, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(repo, "four.txt"), []byte("four\n"), 0o644); err != nil {
		t.Fatalf("write four: %v", err)
	}
	runGit(t, git, repo, "add", ".")
	runGit(t, git, repo, "commit", "-m", "four")
	advanced := gitOutput(t, git, repo, "rev-parse", "HEAD")

	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != previous {
		t.Fatalf("worktree HEAD = %s, want %s (main advanced to %s)", got, previous, advanced)
	}
}

// TestCreateRefusesUnavailablePinnedBaseSHA covers both halves of "unavailable":
// a well-formed id this repository does not have, and values that are not commit
// ids at all. Neither may create anything.
func TestCreateRefusesUnavailablePinnedBaseSHA(t *testing.T) {
	git := requireGit(t)
	repo, _, _ := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	otherRepoTmp := t.TempDir()
	otherRepo, otherHead, _ := pinnedTestRepoIn(t, git, otherRepoTmp)
	_ = otherRepo

	for _, tc := range []struct {
		name  string
		value string
		want  error
	}{
		{"unknown commit", strings.Repeat("0", 40), ports.ErrWorkspaceBaseSHAUnavailable},
		{"not hex", "not-a-sha", ports.ErrWorkspaceBaseSHAUnavailable},
		{"ref expression", "HEAD~1", ports.ErrWorkspaceBaseSHAUnavailable},
		{"whitespace only", "   ", ports.ErrWorkspaceBaseSHAUnavailable},
		{"abbreviated", strings.Repeat("a", 7), ports.ErrWorkspaceBaseSHAUnavailable},
		{"a commit of another repository", otherHead, ports.ErrWorkspaceBaseSHAUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ws.Create(ctx, ports.WorkspaceConfig{
				ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: tc.value,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(ws.managedRoot, "proj", "sess")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused pin left a worktree path: %v", statErr)
			}
			if branchExists(t, git, repo, "ao/pinned") {
				t.Fatal("a refused pin created the session branch")
			}
		})
	}
}

// pinnedTestRepoIn builds a three-commit repo under an explicit parent dir.
func pinnedTestRepoIn(t *testing.T, git, parent string) (repo, head, previous string) {
	t.Helper()
	repo = filepath.Join(parent, "repo")
	run(t, git, "init", repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(repo, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		runGit(t, git, repo, "add", ".")
		runGit(t, git, repo, "commit", "-m", name)
	}
	runGit(t, git, repo, "branch", "-M", "main")
	return repo, gitOutput(t, git, repo, "rev-parse", "HEAD"), gitOutput(t, git, repo, "rev-parse", "HEAD~1")
}

// TestCreatePinnedBaseSHARejectsExistingBranchAtAnotherCommit is the idempotent
// retry rule: an existing session branch is reused only while it is still at the
// pin. A branch that moved is refused rather than silently checked out.
func TestCreatePinnedBaseSHARejectsExistingBranchAtAnotherCommit(t *testing.T) {
	git := requireGit(t)
	repo, head, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	// The branch exists at the TIP, not at the pin.
	runGit(t, git, repo, "branch", "ao/pinned", head)

	_, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if !errors.Is(err, ports.ErrWorkspaceBaseSHAMismatch) {
		t.Fatalf("error = %v, want ports.ErrWorkspaceBaseSHAMismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(ws.managedRoot, "proj", "sess")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused pinned reuse left a worktree path: %v", statErr)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "refs/heads/ao/pinned"); got != head {
		t.Fatalf("the existing branch moved to %s, want it untouched at %s", got, head)
	}
}

// TestCreatePinnedBaseSHAAcceptsExistingBranchAtThePin is the accepted half of
// that rule: a branch already at the pin is reused idempotently.
func TestCreatePinnedBaseSHAAcceptsExistingBranchAtThePin(t *testing.T) {
	git := requireGit(t)
	repo, _, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	runGit(t, git, repo, "branch", "ao/pinned", previous)

	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if err != nil {
		t.Fatalf("create reusing a branch at the pin: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != previous {
		t.Fatalf("worktree HEAD = %s, want the pin %s", got, previous)
	}
	if info.BaseSHA != previous {
		t.Fatalf("info.BaseSHA = %q, want %q", info.BaseSHA, previous)
	}
}

// TestCreatePinnedBaseSHARefusesExistingWorktreeAtAnotherCommit covers the
// re-attach case: a live worktree at the managed path that is NOT at the pin must
// be refused instead of handed back as if it were.
func TestCreatePinnedBaseSHARefusesExistingWorktreeAtAnotherCommit(t *testing.T) {
	git := requireGit(t)
	repo, head, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	// First create pins the worktree at the historical commit.
	first, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if got := gitOutput(t, git, first.Path, "rev-parse", "HEAD"); got != previous {
		t.Fatalf("first worktree HEAD = %s, want %s", got, previous)
	}

	// Move the worktree's branch to the tip out of band, then Create again with
	// the same pin: the path now holds something that is not the pinned commit.
	runGit(t, git, first.Path, "reset", "--hard", head)
	_, err = ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if !errors.Is(err, ports.ErrWorkspaceBaseSHAMismatch) {
		t.Fatalf("error = %v, want ports.ErrWorkspaceBaseSHAMismatch", err)
	}
	// The existing worktree was not moved, reset, or deleted.
	if got := gitOutput(t, git, first.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the refused worktree moved to %s, want it left at %s", got, head)
	}
}

// TestCreateWithoutPinnedBaseSHAKeepsDefaultBehaviour is the no-regression half:
// with no pin, the branch still comes from the resolved default ref.
func TestCreateWithoutPinnedBaseSHAKeepsDefaultBehaviour(t *testing.T) {
	git := requireGit(t)
	repo, head, _ := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	// This fixture has no remote, so the default is named explicitly: the test is
	// about "no pin", not about automatic default resolution (covered elsewhere).
	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/default", BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("create without a pin: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("worktree HEAD = %s, want the default tip %s", got, head)
	}
}

// TestWorktreeHeadReportsCheckedOutCommit is a guard on the helper the pinned
// re-attach path uses: it must report the same commit git does.
func TestWorktreeHeadReportsCheckedOutCommit(t *testing.T) {
	git := requireGit(t)
	repo, head, previous := pinnedTestRepo(t, git)
	ws := newPinnedWorkspace(t, git, repo)
	ctx := context.Background()

	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "ao/pinned", BaseSHA: previous,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := ws.worktreeHead(ctx, info.Path)
	if err != nil || got != previous {
		t.Fatalf("worktreeHead = %q err=%v, want %q", got, err, previous)
	}
	// A pinned worktree never silently becomes the tip.
	if got == head {
		t.Fatalf("worktreeHead reported the repository tip %s instead of the pin %s", head, previous)
	}
}

// branchExists reports whether refs/heads/<branch> exists in repo.
func branchExists(t *testing.T, git, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}
