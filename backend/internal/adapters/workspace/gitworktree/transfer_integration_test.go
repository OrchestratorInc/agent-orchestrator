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

func TestTransferBundleFetchAlign(t *testing.T) {
	ctx := context.Background()
	const branch, remotePreserved = "feature/move", "refs/ao/preserved/tr12345678"
	git, tmp, repoA, repoB, wsA, wsB, info := transferPair(t, branch)
	bundle := filepath.Join(tmp, "move.bundle")
	commit := func(name string) {
		writeFile(t, info.Path, name, name+"\n")
		runGit(t, git, info.Path, "add", name)
		runGit(t, git, info.Path, "commit", "-m", name)
	}
	commit("one.txt")
	writeFile(t, info.Path, "draft.txt", "draft\n")
	ref, err := wsA.StashUncommitted(ctx, info)
	if err != nil || ref == "" {
		t.Fatalf("stash = %q, %v", ref, err)
	}
	head, uncommitted, bundled, err := wsA.BundleTransfer(ctx, info, ref, remotePreserved, bundle)
	if err != nil || !uncommitted || !bundled {
		t.Fatalf("bundle = %v %v, %v", uncommitted, bundled, err)
	}
	if want := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); head != want {
		t.Fatalf("bundle head = %s, want %s", head, want)
	}
	if out := gitOutput(t, git, filepath.Join(tmp, "origin.git"), "for-each-ref"); strings.Contains(out, "refs/ao/") || strings.Contains(out, branch) {
		t.Fatalf("origin got the work:\n%s", out)
	}
	if out := gitOutput(t, git, info.Path, "for-each-ref", remotePreserved+"*"); out != "" {
		t.Fatalf("source kept %s", out)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, head, bundle, remotePreserved); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", remotePreserved+"^"); got != head {
		t.Fatalf("preserved parent = %s, want %s", got, head)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head); err != nil {
		t.Fatalf("align new branch: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", "refs/heads/"+branch); got != head {
		t.Fatalf("b branch = %s, want %s", got, head)
	}

	// Pushed and clean: nothing to bundle, and the head comes from origin.
	runGit(t, git, info.Path, "checkout", "--", ".")
	runGit(t, git, info.Path, "clean", "-fdq")
	commit("two.txt")
	runGit(t, git, info.Path, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	_ = os.Remove(bundle)
	head2, uncommitted, bundled, err := wsA.BundleTransfer(ctx, info, "", remotePreserved, bundle)
	if err != nil || uncommitted || bundled {
		t.Fatalf("clean pushed bundle = %v %v, %v; want none", uncommitted, bundled, err)
	}
	if _, err := os.Stat(bundle); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a bundle was written: %v", err)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, head2, "", ""); err != nil {
		t.Fatalf("fetch from origin: %v", err)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head2); err != nil {
		t.Fatalf("fast-forward: %v", err)
	}

	// Pushed with a draft: the bundle carries the draft and not the branch.
	writeFile(t, info.Path, "draft2.txt", "draft\n")
	draft, err := wsA.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, bundled, err := wsA.BundleTransfer(ctx, info, draft, remotePreserved, bundle); err != nil || !bundled {
		t.Fatalf("pushed with a draft = %v, %v", bundled, err)
	}
	if heads := gitOutput(t, git, info.Path, "bundle", "list-heads", bundle); strings.Contains(heads, "refs/heads/") {
		t.Fatalf("the bundle carries the branch:\n%s", heads)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, head2, bundle, remotePreserved); err != nil {
		t.Fatalf("fetch a draft on a pushed branch: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", remotePreserved+"^"); got != head2 {
		t.Fatalf("draft builds on %s, want %s", got, head2)
	}
	runGit(t, git, info.Path, "clean", "-fdq")

	// The bundle builds on a commit B lacks: B fetches it from origin.
	commit("three.txt")
	runGit(t, git, info.Path, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	commit("four.txt")
	head4, _, bundled, err := wsA.BundleTransfer(ctx, info, "", remotePreserved, bundle)
	if err != nil || !bundled {
		t.Fatalf("bundle = %v, %v", bundled, err)
	}
	if err := wsB.FetchTransfer(ctx, repoB, branch, head4, bundle, ""); err != nil {
		t.Fatalf("fetch with a missing base: %v", err)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head4); err != nil {
		t.Fatalf("align after base fetch: %v", err)
	}
	if got := gitOutput(t, git, repoB, "rev-parse", "refs/heads/"+branch); got != head4 {
		t.Fatalf("b branch = %s, want %s", got, head4)
	}

	tree := gitOutput(t, git, repoB, "rev-parse", head4+"^{tree}")
	local := gitOutput(t, git, repoB, "commit-tree", tree, "-p", head4, "-m", "only on b")
	runGit(t, git, repoB, "update-ref", "refs/heads/"+branch, local)
	if err := wsB.AlignTransferBranch(ctx, repoB, branch, head4); !errors.Is(err, ports.ErrTransferBranchDiverged) {
		t.Fatalf("align with local-only commit err = %v, want ErrTransferBranchDiverged", err)
	}

	if err := wsA.ReleaseTransferBranch(ctx, info); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != head4 {
		t.Fatalf("released HEAD = %s, want %s", got, head4)
	}
	runGit(t, git, repoA, "worktree", "add", filepath.Join(tmp, "again"), branch)
	if _, _, _, err := wsA.BundleTransfer(ctx, info, "", remotePreserved, bundle); !errors.Is(err, ports.ErrDetachedHead) {
		t.Fatalf("detached bundle err = %v, want ErrDetachedHead", err)
	}
}

func TestFetchTransferNamesMissingWork(t *testing.T) {
	ctx := context.Background()
	git, tmp, _, repoB, wsA, wsB, info := transferPair(t, "feature/missing")
	writeFile(t, info.Path, "one.txt", "one\n")
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-qm", "one")
	head := gitOutput(t, git, info.Path, "rev-parse", "HEAD")
	if err := wsB.FetchTransfer(ctx, repoB, "feature/missing", head, "", ""); !errors.Is(err, ports.ErrTransferFetch) {
		t.Fatalf("unpushed head without a bundle: %v, want ErrTransferFetch", err)
	}
	// A bundle whose base neither B nor origin has.
	writeFile(t, info.Path, "two.txt", "two\n")
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-qm", "two")
	bundle := filepath.Join(tmp, "partial.bundle")
	runGit(t, git, info.Path, "bundle", "create", bundle, "refs/heads/feature/missing", "^"+head)
	if err := wsB.FetchTransfer(ctx, repoB, "feature/missing", gitOutput(t, git, info.Path, "rev-parse", "HEAD"), bundle, ""); !errors.Is(err, ports.ErrTransferFetch) {
		t.Fatalf("bundle with an unknown base: %v, want ErrTransferFetch", err)
	}
	_ = wsA
}

func TestBundleTransferRefusesUnpushedLFSFiles(t *testing.T) {
	ctx := context.Background()
	git, tmp, _, _, wsA, _, info := transferPair(t, "feature/lfs")
	writeFile(t, info.Path, "big.psd", "version https://git-lfs.github.com/spec/v1\noid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12345\n")
	if _, _, _, err := wsA.BundleTransfer(ctx, info, mustStash(t, wsA, info), "refs/ao/preserved/tr12345678", filepath.Join(tmp, "lfs.bundle")); !errors.Is(err, ports.ErrTransferLFS) {
		t.Fatalf("uncommitted LFS file: %v, want ErrTransferLFS", err)
	}
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-qm", "lfs")
	if _, _, _, err := wsA.BundleTransfer(ctx, info, "", "refs/ao/preserved/tr12345678", filepath.Join(tmp, "lfs.bundle")); !errors.Is(err, ports.ErrTransferLFS) {
		t.Fatalf("unpushed LFS commit: %v, want ErrTransferLFS", err)
	}
	runGit(t, git, info.Path, "push", "-q", "origin", "HEAD:refs/heads/feature/lfs")
	if _, _, bundled, err := wsA.BundleTransfer(ctx, info, "", "refs/ao/preserved/tr12345678", filepath.Join(tmp, "lfs.bundle")); err != nil || bundled {
		t.Fatalf("pushed LFS commit = %v, %v; want no bundle, no error", bundled, err)
	}
}

func mustStash(t *testing.T, ws *Workspace, info ports.WorkspaceInfo) string {
	t.Helper()
	ref, err := ws.StashUncommitted(context.Background(), info)
	if err != nil || ref == "" {
		t.Fatalf("stash = %q, %v", ref, err)
	}
	return ref
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

// moveToB moves info's worktree through origin into a worktree of repoB.
func moveToB(t *testing.T, wsA, wsB *Workspace, repoB string, info ports.WorkspaceInfo, remoteRef string) ports.WorkspaceInfo {
	t.Helper()
	ctx := context.Background()
	ref, err := wsA.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("stash: %v", err)
	}
	bundle := filepath.Join(t.TempDir(), "move.bundle")
	head, uncommitted, bundled, err := wsA.BundleTransfer(ctx, info, ref, remoteRef, bundle)
	if err != nil || !uncommitted || !bundled {
		t.Fatalf("bundle = %v %v, %v", uncommitted, bundled, err)
	}
	if err := wsB.FetchTransfer(ctx, repoB, info.Branch, head, bundle, remoteRef); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := wsB.AlignTransferBranch(ctx, repoB, info.Branch, head); err != nil {
		t.Fatalf("align: %v", err)
	}
	if parent, err := wsB.RevParse(ctx, repoB, remoteRef+"^"); err != nil || parent != head {
		t.Fatalf("preserved parent = %q, %v; want %s", parent, err, head)
	}
	moved, err := wsB.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "moved", Branch: info.Branch})
	if err != nil {
		t.Fatalf("create moved: %v", err)
	}
	if err := wsB.ApplyPreserved(ctx, moved, remoteRef); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := wsB.RestoreTransferIndex(ctx, moved, remoteRef); err != nil {
		t.Fatalf("restore index: %v", err)
	}
	return moved
}

func TestTransferCarriesAStagedEditUndoneOnDisk(t *testing.T) {
	git, _, _, repoB, wsA, wsB, info := transferPair(t, "feature/undone")
	writeFile(t, info.Path, "f.txt", "v1\n")
	runGit(t, git, info.Path, "add", "-A")
	runGit(t, git, info.Path, "commit", "-m", "base")
	writeFile(t, info.Path, "f.txt", "staged\n")
	runGit(t, git, info.Path, "add", "f.txt")
	writeFile(t, info.Path, "f.txt", "v1\n")

	moved := moveToB(t, wsA, wsB, repoB, info, "refs/ao/preserved/tr12345678")
	if got := gitOutput(t, git, moved.Path, "status", "--porcelain"); got != "MM f.txt" {
		t.Fatalf("status = %q, want MM f.txt", got)
	}
	if got := gitOutput(t, git, moved.Path, "show", ":f.txt"); got != "staged" {
		t.Fatalf("staged f.txt = %q", got)
	}
}

func TestTransferKeepsTrackedFilesGitIgnores(t *testing.T) {
	git, _, _, repoB, wsA, wsB, info := transferPair(t, "feature/ignored")
	writeFile(t, info.Path, ".gitignore", "*.log\n")
	writeFile(t, info.Path, "tracked.log", "kept\n")
	writeFile(t, info.Path, "sparse.txt", "left out of this checkout\n")
	runGit(t, git, info.Path, "add", "-f", "-A", ".")
	runGit(t, git, info.Path, "commit", "-m", "base")
	writeFile(t, info.Path, "README.md", "only edit\n")
	writeFile(t, info.Path, "new.log", "forced\n")
	runGit(t, git, info.Path, "add", "-f", "new.log")
	runGit(t, git, info.Path, "update-index", "--skip-worktree", "sparse.txt")
	if err := os.Remove(filepath.Join(info.Path, "sparse.txt")); err != nil {
		t.Fatal(err)
	}

	moved := moveToB(t, wsA, wsB, repoB, info, "refs/ao/preserved/tr12345678")
	if got, want := gitOutput(t, git, moved.Path, "status", "--porcelain"), "M README.md\nA  new.log"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	for name, want := range map[string]string{"tracked.log": "kept\n", "new.log": "forced\n", "sparse.txt": "left out of this checkout\n"} {
		if got, err := os.ReadFile(filepath.Join(moved.Path, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
}

func TestCheckTransferRefusesSubmoduleWork(t *testing.T) {
	git, tmp, _, _, wsA, _, info := transferPair(t, "feature/submodule")
	ctx := context.Background()
	lib := filepath.Join(tmp, "lib.git")
	run(t, git, "clone", "--bare", "-q", filepath.Join(tmp, "origin.git"), lib)
	runGit(t, git, info.Path, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "libs/core")
	runGit(t, git, info.Path, "commit", "-qm", "add submodule")
	sub := filepath.Join(info.Path, "libs", "core")
	runGit(t, git, sub, "config", "user.email", "ao@example.com")
	runGit(t, git, sub, "config", "user.name", "Ao Agents")
	check := func(step string, want bool) {
		t.Helper()
		err := wsA.CheckTransfer(ctx, info)
		if refused := errors.Is(err, ports.ErrTransferSubmodule) && strings.Contains(err.Error(), "libs/core"); refused != want || (!want && err != nil) {
			t.Fatalf("%s: err = %v, want refused %v", step, err, want)
		}
	}
	check("clean submodule", false)
	writeFile(t, sub, "README.md", "edited\n")
	check("edited submodule", true)
	runGit(t, git, sub, "commit", "-qam", "local")
	check("unpushed submodule commit", true)
	runGit(t, git, info.Path, "commit", "-qam", "point at it")
	check("unpushed commit the branch points at", true)
	runGit(t, git, sub, "push", "-q", "origin", "HEAD:refs/heads/pushed")
	check("pushed submodule commit", false)

	// A shallow checkout of a commit that no fetched branch holds.
	pinned := gitOutput(t, git, sub, "rev-parse", "HEAD")
	runGit(t, git, sub, "fetch", "-q", "--depth=1", "origin", "refs/heads/pushed")
	runGit(t, git, sub, "update-ref", "-d", "refs/remotes/origin/pushed")
	check("shallow submodule at "+pinned, false)
	writeFile(t, sub, "README.md", "more\n")
	runGit(t, git, sub, "commit", "-qam", "local again")
	check("local commit in a shallow submodule", true)
}

func TestTransferCarriesStagedState(t *testing.T) {
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

	moved := moveToB(t, wsA, wsB, repoB, info, remoteRef)
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
	if out := gitOutput(t, git, repoA, "ls-remote", "origin"); strings.Contains(out, "refs/ao/") {
		t.Fatalf("origin got %s", out)
	}
}

func TestCheckTransferRefusesUnfinishedGit(t *testing.T) {
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
	if err := wsA.CheckTransfer(ctx, info); !errors.Is(err, ports.ErrTransferGitBusy) || !strings.Contains(err.Error(), "merge") {
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
	if err := wsA.CheckTransfer(ctx, info); !errors.Is(err, ports.ErrTransferGitBusy) || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unresolved conflicts: %v, want ErrTransferGitBusy", err)
	}
}

func TestCheckTransferRefusesNestedRepo(t *testing.T) {
	git, _, _, _, wsA, _, info := transferPair(t, "feature/nested")
	inner := filepath.Join(info.Path, "deep", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, inner, "init", "-q")
	writeFile(t, inner, "n.txt", "n\n")
	if err := wsA.CheckTransfer(context.Background(), info); !errors.Is(err, ports.ErrTransferNestedRepo) || !strings.Contains(err.Error(), "deep/inner") {
		t.Fatalf("nested repository: %v, want ErrTransferNestedRepo naming deep/inner", err)
	}
}

func TestGitSaidHidesCredentials(t *testing.T) {
	if got := gitSaid([]byte("fatal: unable to access 'https://me:secret@example.com/x.git/'")); strings.Contains(got, "secret") {
		t.Fatalf("gitSaid leaks credentials: %s", got)
	}
}
