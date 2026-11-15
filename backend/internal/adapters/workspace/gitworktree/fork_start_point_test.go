package gitworktree

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestForkWorktreeStartsAtSourceCommit(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	// An unpushed commit on a side branch: the fork must start from it, not
	// from the project's base branch.
	runGit(t, git, repo, "checkout", "-q", "-b", "source")
	runGit(t, git, repo, "commit", "-q", "--allow-empty", "-m", "unpushed")
	source := gitOutput(t, git, repo, "rev-parse", "HEAD")
	runGit(t, git, repo, "checkout", "-q", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "fork1", Branch: "ao/fork1/root", FreshBranch: true, StartPoint: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != source {
		t.Fatalf("fork HEAD = %s, want %s", got, source)
	}
	if info.BaseSHA != source {
		t.Fatalf("BaseSHA = %s, want %s", info.BaseSHA, source)
	}
	if info.BaseRef == "" {
		t.Fatal("BaseRef should still name the project's base branch")
	}
}
