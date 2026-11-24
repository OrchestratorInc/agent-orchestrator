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

func TestRestoreTransferIndexUnstagesWhenNothingWasStaged(t *testing.T) {
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
	if err := ws.RestoreTransferIndex(ctx, info, ref); err != nil {
		t.Fatalf("restore index: %v", err)
	}
	if got, want := gitOutput(t, git, info.Path, "status", "--porcelain"), "M README.md\n?? draft.md"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

// transferPair is two clones of one origin and a session worktree in the first.
func transferPair(t *testing.T, branch string) (git, tmp, repoA, repoB string, wsA, wsB *Workspace, info ports.WorkspaceInfo) {
	t.Helper()
	git = requireGit(t)
	tmp = t.TempDir()
	repoA = setupOriginClone(t, git, tmp)
	repoB = filepath.Join(tmp, "repo-b")
	run(t, git, "clone", filepath.Join(tmp, "origin.git"), repoB)
	runGit(t, git, repoB, "config", "user.email", "ao@example.com")
	runGit(t, git, repoB, "config", "user.name", "Ao Agents")
	var err error
	if wsA, err = New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed-a"), RepoResolver: StaticRepoResolver{"proj": repoA}}); err != nil {
		t.Fatal(err)
	}
	if wsB, err = New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed-b"), RepoResolver: StaticRepoResolver{"proj": repoB}}); err != nil {
		t.Fatal(err)
	}
	info, err = wsA.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: branch})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return git, tmp, repoA, repoB, wsA, wsB, info
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTransferCarriesStagedState(t *testing.T) {
	ctx := context.Background()
	const branch, remoteRef = "feature/staged", "refs/ao/preserved/tr12345678"
	git, _, repoA, repoB, wsA, wsB, info := transferPair(t, branch)
	for _, name := range []string{"keep.txt", "gone.txt", "old.txt"} {
		writeFile(t, info.Path, name, "v1\n")
	}
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-m", "base")
	// Partly staged, staged new, staged delete, staged rename, unstaged edit, untracked.
	writeFile(t, info.Path, "keep.txt", "staged\n")
	runGit(t, git, info.Path, "add", "keep.txt")
	writeFile(t, info.Path, "keep.txt", "staged then more\n")
	writeFile(t, info.Path, "new.txt", "new\n")
	runGit(t, git, info.Path, "add", "new.txt")
	runGit(t, git, info.Path, "rm", "-q", "gone.txt")
	runGit(t, git, info.Path, "mv", "old.txt", "renamed.txt")
	writeFile(t, info.Path, "README.md", "unstaged edit\n")
	writeFile(t, info.Path, "draft.md", "draft\n")
	wantStatus := gitOutput(t, git, info.Path, "status", "--porcelain")
	wantStaged := gitOutput(t, git, info.Path, "show", ":keep.txt")

	ref, err := wsA.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("stash = %q, %v", ref, err)
	}
	head, err := wsA.PushTransfer(ctx, info, ref, remoteRef)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, remoteRef); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head); err != nil {
		t.Fatalf("align: %v", err)
	}
	moved, err := wsB.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "moved", Branch: branch})
	if err != nil {
		t.Fatalf("create moved: %v", err)
	}
	if err := wsB.ApplyPreserved(ctx, moved, remoteRef); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := wsB.RestoreTransferIndex(ctx, moved, remoteRef); err != nil {
		t.Fatalf("restore index: %v", err)
	}
	if got := gitOutput(t, git, moved.Path, "status", "--porcelain"); got != wantStatus {
		t.Fatalf("status = %q, want %q", got, wantStatus)
	}
	if got := gitOutput(t, git, moved.Path, "show", ":keep.txt"); got != wantStaged {
		t.Fatalf("staged keep.txt = %q, want %q", got, wantStaged)
	}
	if got, err := os.ReadFile(filepath.Join(moved.Path, "keep.txt")); err != nil || string(got) != "staged then more\n" {
		t.Fatalf("keep.txt = %q, %v", got, err)
	}
	if out := gitOutput(t, git, repoB, "for-each-ref", "refs/ao/"); out != "" {
		t.Fatalf("destination kept %s", out)
	}
	if err := wsA.DeleteTransferRef(ctx, repoA, remoteRef); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if out := gitOutput(t, git, repoA, "ls-remote", "origin", "refs/ao/*"); out != "" {
		t.Fatalf("origin kept %s", out)
	}
}

func TestPushTransferRefusesUnfinishedGit(t *testing.T) {
	ctx := context.Background()
	git, _, _, _, wsA, _, info := transferPair(t, "feature/merge")
	writeFile(t, info.Path, "m.txt", "base\n")
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-m", "base")
	runGit(t, git, info.Path, "branch", "other")
	writeFile(t, info.Path, "m.txt", "mine\n")
	runGit(t, git, info.Path, "commit", "-am", "mine")
	runGit(t, git, info.Path, "switch", "-q", "other")
	writeFile(t, info.Path, "m.txt", "theirs\n")
	runGit(t, git, info.Path, "commit", "-am", "theirs")
	runGit(t, git, info.Path, "switch", "-q", "feature/merge")
	_ = exec.Command(git, "-C", info.Path, "merge", "other").Run()
	if _, err := wsA.PushTransfer(ctx, info, "", ""); !errors.Is(err, ports.ErrTransferGitBusy) || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("merge in progress: %v, want ErrTransferGitBusy", err)
	}
	// Conflicts without a merge, as after a stash pop, refuse as well.
	runGit(t, git, info.Path, "merge", "--abort")
	runGit(t, git, info.Path, "update-index", "--index-info")
	blob := gitOutput(t, git, info.Path, "rev-parse", "HEAD:m.txt")
	in := "100644 " + blob + " 1\tm.txt\n100644 " + blob + " 2\tm.txt\n100644 " + blob + " 3\tm.txt\n"
	cmd := exec.Command(git, "-C", info.Path, "update-index", "--index-info")
	cmd.Stdin = strings.NewReader("0 0000000000000000000000000000000000000000\tm.txt\n" + in)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("index-info: %v: %s", err, out)
	}
	if _, err := wsA.PushTransfer(ctx, info, "", ""); !errors.Is(err, ports.ErrTransferGitBusy) || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unresolved conflicts: %v, want ErrTransferGitBusy", err)
	}
}

func TestPushTransferRefusesNestedRepo(t *testing.T) {
	git, _, _, _, wsA, _, info := transferPair(t, "feature/nested")
	inner := filepath.Join(info.Path, "deep", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, inner, "init", "-q")
	writeFile(t, inner, "n.txt", "n\n")
	if _, err := wsA.PushTransfer(context.Background(), info, "", ""); !errors.Is(err, ports.ErrTransferNestedRepo) || !strings.Contains(err.Error(), "deep/inner") {
		t.Fatalf("nested repository: %v, want ErrTransferNestedRepo naming deep/inner", err)
	}
}

func TestPushTransferReportsARefusedPush(t *testing.T) {
	git, _, repoA, _, wsA, _, info := transferPair(t, "feature/rewritten")
	ctx := context.Background()
	writeFile(t, info.Path, "one.txt", "one\n")
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-m", "one")
	if _, err := wsA.PushTransfer(ctx, info, "", ""); err != nil {
		t.Fatalf("push: %v", err)
	}
	runGit(t, git, info.Path, "commit", "--amend", "-m", "rewritten")
	_, err := wsA.PushTransfer(ctx, info, "", "")
	if !errors.Is(err, ports.ErrTransferPush) || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("rewritten history: %v, want ErrTransferPush with git's reason", err)
	}
	_ = repoA
}

func TestGitSaidHidesCredentials(t *testing.T) {
	if got := gitSaid([]byte("fatal: unable to access 'https://me:secret@example.com/x.git/'")); strings.Contains(got, "secret") {
		t.Fatalf("gitSaid leaks credentials: %s", got)
	}
}
