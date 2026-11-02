package filesnapshot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRestoreReturnsWorktreeAndIndexToTheSnapshot(t *testing.T) {
	repo := newRepo(t)
	// Before the turn the user had staged one edit and left one new file untracked.
	write(t, repo, "a.txt", "staged\n")
	runGit(t, repo, "add", "a.txt")
	write(t, repo, "notes.txt", "untracked\n")
	write(t, repo, "build/out.bin", "ignored\n")
	before := status(t, repo)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The turn edits, deletes, stages, creates a directory and touches ignored output.
	write(t, repo, "a.txt", "agent\n")
	remove(t, repo, "b.txt")
	write(t, repo, "new.txt", "agent\n")
	runGit(t, repo, "add", "new.txt")
	write(t, repo, "pkg/deep/file.go", "package deep\n")
	remove(t, repo, "notes.txt")
	write(t, repo, "build/out.bin", "rebuilt\n")

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []Change{
		{Path: "a.txt", Kind: ChangeReverted},
		{Path: "b.txt", Kind: ChangeRestored},
		{Path: "new.txt", Kind: ChangeRemoved},
		{Path: "notes.txt", Kind: ChangeRestored},
		{Path: "pkg/deep/file.go", Kind: ChangeRemoved},
	}
	if !reflect.DeepEqual(plan.Changes, want) || plan.CommitsSince != 0 {
		t.Fatalf("plan = %+v, commits %d; want %+v, 0", plan.Changes, plan.CommitsSince, want)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := status(t, repo); got != before {
		t.Fatalf("status after restore = %q, want the pre-turn %q", got, before)
	}
	for path, content := range map[string]string{"a.txt": "staged\n", "b.txt": "b\n", "notes.txt": "untracked\n", "build/out.bin": "rebuilt\n"} {
		if got := read(t, repo, path); got != content {
			t.Fatalf("%s = %q, want %q", path, got, content)
		}
	}
	if got := gitOutput(t, repo, "show", ":a.txt"); got != "staged" {
		t.Fatalf("staged a.txt = %q, want the pre-turn staged contents", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "pkg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory the turn created survived the restore: %v", err)
	}
}

func TestUndoPutsBackWhatApplyReplaced(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "agent\n")
	remove(t, repo, "b.txt")
	write(t, repo, "new.txt", "agent\n")
	runGit(t, repo, "add", "new.txt")
	after := status(t, repo)

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := plan.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	if got := status(t, repo); got != after {
		t.Fatalf("status after undo = %q, want %q", got, after)
	}
	if got := read(t, repo, "new.txt"); got != "agent\n" {
		t.Fatalf("new.txt = %q after undo", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("b.txt came back after undo: %v", err)
	}
}

func TestRestoreSwapsFilesAndDirectoriesBothWays(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	remove(t, repo, "a.txt")
	write(t, repo, "a.txt/inner.txt", "now a directory\n")

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := read(t, repo, "a.txt"); got != "a\n" {
		t.Fatalf("a.txt = %q, want the file back", got)
	}
	if err := plan.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := read(t, repo, "a.txt/inner.txt"); got != "now a directory\n" {
		t.Fatalf("a.txt/inner.txt = %q, want the directory back", got)
	}
}

func TestCaptureLeavesBranchIndexAndStashAlone(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "a.txt", "staged\n")
	runGit(t, repo, "add", "a.txt")
	write(t, repo, "a.txt", "unstaged\n")
	write(t, repo, "notes.txt", "untracked\n")
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	index := read(t, repo, ".git/index")
	before := status(t, repo)

	first, second := Ref("s1", "turn-1"), Ref("s1", "turn-2")
	if err := Capture(context.Background(), repo, first, second); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if gitOutput(t, repo, "rev-parse", "HEAD") != head || read(t, repo, ".git/index") != index || status(t, repo) != before {
		t.Fatal("Capture changed HEAD, the index or the worktree")
	}
	if stashes := gitOutput(t, repo, "stash", "list"); stashes != "" {
		t.Fatalf("Capture pushed a stash: %q", stashes)
	}
	if gitOutput(t, repo, "rev-parse", first) != gitOutput(t, repo, "rev-parse", second) {
		t.Fatal("refs captured together point at different snapshots")
	}
	if got := gitOutput(t, repo, "show", first+":a.txt"); got != "unstaged" {
		t.Fatalf("snapshot a.txt = %q, want the worktree contents", got)
	}
	if got := gitOutput(t, repo, "show", first+"^2:a.txt"); got != "staged" {
		t.Fatalf("snapshot index a.txt = %q, want the staged contents", got)
	}
}

func TestSnapshotsRunNoRepositoryHooks(t *testing.T) {
	repo := newRepo(t)
	hooks, marker := t.TempDir(), filepath.Join(t.TempDir(), "fired")
	for _, hook := range []string{"post-index-change", "reference-transaction"} {
		write(t, hooks, hook, "#!/bin/sh\necho "+hook+" >> "+marker+"\n")
		if err := os.Chmod(filepath.Join(hooks, hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "config", "core.hooksPath", hooks)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "agent\n")
	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if fired, err := os.ReadFile(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hooks fired: %q (%v)", fired, err)
	}
}

func TestRestoreLeavesLaterCommitsOnTheBranch(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "committed\n")
	runGit(t, repo, "commit", "-am", "agent change")
	write(t, repo, "c.txt", "committed\n")
	runGit(t, repo, "add", "c.txt")
	runGit(t, repo, "commit", "-m", "agent file")
	head := gitOutput(t, repo, "rev-parse", "HEAD")

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.CommitsSince != 2 {
		t.Fatalf("CommitsSince = %d, want 2", plan.CommitsSince)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := gitOutput(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s, want the branch left at %s", got, head)
	}
	if got := read(t, repo, "a.txt"); got != "a\n" {
		t.Fatalf("a.txt = %q, want the pre-turn contents", got)
	}
	if got := status(t, repo); got != "M  a.txt\nD  c.txt" {
		t.Fatalf("status = %q, want the commits' reversal", got)
	}
}

func TestPlanErrors(t *testing.T) {
	repo := newRepo(t)
	if _, err := Plan(context.Background(), repo, Ref("s1", "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Plan(missing ref) = %v, want ErrNotFound", err)
	}
	if _, err := Plan(context.Background(), t.TempDir(), Ref("s1", "turn-1")); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Plan(plain directory) = %v, want ErrNotRepository", err)
	}
	if err := Capture(context.Background(), t.TempDir(), Ref("s1", "turn-1")); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Capture(plain directory) = %v, want ErrNotRepository", err)
	}
}

func TestPlanRefusesAWorktreeHoldingAnotherRepository(t *testing.T) {
	// A workspace project keeps child repositories at ignored paths in its root.
	repo := newRepo(t)
	write(t, repo, ".gitignore", "build/\n/services/api/\n")
	runGit(t, repo, "commit", "-qam", "ignore child")
	child := filepath.Join(repo, "services", "api")
	runGit(t, repo, "init", "-q", child)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := Plan(context.Background(), repo, ref); !errors.Is(err, ErrNestedRepository) {
		t.Fatalf("Plan(ignored child repository) = %v, want ErrNestedRepository", err)
	}

	// A repository the turn cloned into the worktree shows up as a gitlink.
	repo = newRepo(t)
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	embedded := filepath.Join(repo, "vendor", "lib")
	runGit(t, repo, "init", "-q", embedded)
	write(t, embedded, "lib.go", "package lib\n")
	runGit(t, embedded, "add", ".")
	runGit(t, embedded, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-qm", "lib")
	if _, err := Plan(context.Background(), repo, ref); !errors.Is(err, ErrNestedRepository) {
		t.Fatalf("Plan(embedded repository) = %v, want ErrNestedRepository", err)
	}
}

func TestCaptureRefusesAWorkspaceInsideAnotherRepository(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "nested/file.txt", "x\n")
	nested := filepath.Join(repo, "nested")

	if err := Capture(context.Background(), nested, Ref("s1", "turn-1")); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Capture(nested directory) = %v, want ErrNotRepository", err)
	}
	if _, err := Plan(context.Background(), nested, Ref("s1", "turn-1")); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Plan(nested directory) = %v, want ErrNotRepository", err)
	}
	if refs := gitOutput(t, repo, "for-each-ref", "refs/ao/"); refs != "" {
		t.Fatalf("the enclosing repository gained refs: %q", refs)
	}
}

func TestDeleteAllRemovesOnlyThatSessionsSnapshots(t *testing.T) {
	repo := newRepo(t)
	worktree := filepath.Join(t.TempDir(), "session")
	runGit(t, repo, "worktree", "add", "-b", "session", worktree)
	if err := Capture(context.Background(), worktree, Ref("s1", "turn-1"), Ref("s1", "turn-2"), Ref("s10", "turn-1")); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if err := DeleteAll(context.Background(), repo, SessionPrefix("s1")); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if got := gitOutput(t, repo, "for-each-ref", "--format=%(refname)", "refs/ao/snapshots/"); got != Ref("s10", "turn-1") {
		t.Fatalf("snapshot refs left = %q, want only the other session's", got)
	}
	if err := DeleteAll(context.Background(), repo, SessionPrefix("s1")); err != nil {
		t.Fatalf("DeleteAll with nothing left: %v", err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	write(t, repo, "a.txt", "a\n")
	write(t, repo, "b.txt", "b\n")
	write(t, repo, ".gitignore", "build/\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func write(t *testing.T, repo, path, content string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, repo, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func remove(t *testing.T, repo, path string) {
	t.Helper()
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, repo string) string {
	t.Helper()
	return gitOutput(t, repo, "status", "--porcelain", "--untracked-files=all")
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	gitOutput(t, repo, args...)
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
