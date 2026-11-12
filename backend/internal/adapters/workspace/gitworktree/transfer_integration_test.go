package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTransferPushFetchAlign(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repoA := setupOriginClone(t, git, tmp)
	repoB := filepath.Join(tmp, "repo-b")
	run(t, git, "clone", filepath.Join(tmp, "origin.git"), repoB)
	runGit(t, git, repoB, "config", "user.email", "ao@example.com")
	runGit(t, git, repoB, "config", "user.name", "Ao Agents")

	ctx := context.Background()
	wsA, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed-a"), RepoResolver: StaticRepoResolver{"proj": repoA}})
	if err != nil {
		t.Fatalf("new a: %v", err)
	}
	wsB, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed-b"), RepoResolver: StaticRepoResolver{"proj": repoB}})
	if err != nil {
		t.Fatalf("new b: %v", err)
	}
	const branch = "feature/move"
	const remotePreserved = "refs/ao/preserved/tr12345678"
	info, err := wsA.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: branch})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	commit := func(name string) {
		if err := os.WriteFile(filepath.Join(info.Path, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		runGit(t, git, info.Path, "add", name)
		runGit(t, git, info.Path, "commit", "-m", name)
	}
	commit("one.txt")
	tree := gitOutput(t, git, info.Path, "rev-parse", "HEAD^{tree}")
	preserved := gitOutput(t, git, info.Path, "commit-tree", tree, "-p", "HEAD", "-m", "preserved")
	runGit(t, git, info.Path, "update-ref", "refs/ao/local-preserved", preserved)

	head, err := wsA.PushTransfer(ctx, info, "refs/ao/local-preserved", remotePreserved)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if want := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); head != want {
		t.Fatalf("push head = %s, want %s", head, want)
	}

	if err := wsB.FetchTransfer(ctx, repoB, branch, remotePreserved); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got, _ := wsB.RevParse(ctx, repoB, remotePreserved); got != preserved {
		t.Fatalf("fetched preserved = %q, want %s", got, preserved)
	}
	for range 2 {
		if err := wsA.DeleteTransferRef(ctx, repoA, remotePreserved); err != nil {
			t.Fatalf("delete preserved ref: %v", err)
		}
	}
	if out := gitOutput(t, git, filepath.Join(tmp, "origin.git"), "for-each-ref", "refs/ao/"); out != "" {
		t.Fatalf("origin still has %s", out)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head); err != nil {
		t.Fatalf("align new branch: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", "refs/heads/"+branch); got != head {
		t.Fatalf("b branch = %s, want %s", got, head)
	}

	commit("two.txt")
	head2, err := wsA.PushTransfer(ctx, info, "", "")
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, ""); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head2); err != nil {
		t.Fatalf("fast-forward: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", "refs/heads/"+branch); got != head2 {
		t.Fatalf("b branch after fast-forward = %s, want %s", got, head2)
	}

	local := gitOutput(t, git, repoB, "commit-tree", tree, "-p", head2, "-m", "only on b")
	runGit(t, git, repoB, "update-ref", "refs/heads/"+branch, local)
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head2); !errors.Is(err, ports.ErrTransferBranchDiverged) {
		t.Fatalf("align with local-only commit err = %v, want ErrTransferBranchDiverged", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", "refs/heads/"+branch); got != local {
		t.Fatalf("b branch = %s, want local commit %s kept", got, local)
	}

	if err := wsA.ReleaseTransferBranch(ctx, info); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != head2 {
		t.Fatalf("released HEAD = %s, want %s", got, head2)
	}
	runGit(t, git, repoA, "worktree", "add", filepath.Join(tmp, "again"), branch)
	if _, err := wsA.PushTransfer(ctx, info, "", ""); !errors.Is(err, ports.ErrDetachedHead) {
		t.Fatalf("detached push err = %v, want ErrDetachedHead", err)
	}
}

func TestUnstageTransferRestoresUntrackedAndUnstaged(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ctx := context.Background()
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/unstage"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "draft.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("stash = %q, %v", ref, err)
	}
	runGit(t, git, info.Path, "checkout", "--", ".")
	runGit(t, git, info.Path, "clean", "-fdq")
	if err := ws.ApplyPreserved(ctx, info, ref); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := ws.UnstageTransfer(ctx, info); err != nil {
		t.Fatalf("unstage: %v", err)
	}
	if got, want := gitOutput(t, git, info.Path, "status", "--porcelain"), "M README.md\n?? draft.md"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}
