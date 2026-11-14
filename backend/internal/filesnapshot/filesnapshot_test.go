package filesnapshot

import (
	"context"
	"errors"
	"fmt"
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

func TestRestoreUndoesWhatTheTurnOnlyStaged(t *testing.T) {
	repo := newRepo(t)
	// Before the turn: an unstaged edit, an untracked file and an unstaged deletion.
	write(t, repo, "a.txt", "edited\n")
	write(t, repo, "notes.txt", "untracked\n")
	remove(t, repo, "b.txt")
	before := status(t, repo)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The turn stages all three without changing a byte on disk.
	runGit(t, repo, "add", "--all")
	after := status(t, repo)

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []Change{
		{Path: "a.txt", Kind: ChangeReverted},
		{Path: "b.txt", Kind: ChangeReverted},
		{Path: "notes.txt", Kind: ChangeReverted},
	}
	if !reflect.DeepEqual(plan.Changes, want) {
		t.Fatalf("plan = %+v, want %+v", plan.Changes, want)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := status(t, repo); got != before {
		t.Fatalf("status after restore = %q, want the pre-turn %q", got, before)
	}
	if got := read(t, repo, "a.txt"); got != "edited\n" {
		t.Fatalf("a.txt = %q, want it left as it was", got)
	}
	if err := plan.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := status(t, repo); got != after {
		t.Fatalf("status after undo = %q, want %q", got, after)
	}
}

func TestRestoreKeepsIgnoredFilesItOverwrites(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "note.txt", "before the turn\n")
	write(t, repo, "out/report.txt", "report\n")
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// The turn ignores both paths, edits note.txt and puts a file where out/ was.
	write(t, repo, ".gitignore", "build/\nnote.txt\n/out\n")
	write(t, repo, "note.txt", "edited by the turn\n")
	remove(t, repo, "out/report.txt")
	remove(t, repo, "out")
	write(t, repo, "out", "file where a directory was\n")

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []Change{
		{Path: ".gitignore", Kind: ChangeReverted},
		{Path: "note.txt", Kind: ChangeReverted},
		{Path: "out", Kind: ChangeRemoved},
		{Path: "out/report.txt", Kind: ChangeRestored},
	}
	if !reflect.DeepEqual(plan.Changes, want) {
		t.Fatalf("plan = %+v, want %+v", plan.Changes, want)
	}

	write(t, repo, "note.txt", "edited after the preview\n")
	plan2, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan2.PlanID == plan.PlanID {
		t.Fatal("PlanID did not change after an ignored file the restore overwrites was edited")
	}

	saved := BeforeRollbackRef("s1", "turn-1")
	if err := plan2.Save(context.Background(), saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := plan2.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := read(t, repo, "note.txt"); got != "before the turn\n" {
		t.Fatalf("note.txt = %q, want the pre-turn contents", got)
	}
	if got := read(t, repo, "out/report.txt"); got != "report\n" {
		t.Fatalf("out/report.txt = %q, want it back", got)
	}
	for path, content := range map[string]string{"note.txt": "edited after the preview", "out": "file where a directory was"} {
		if got := gitOutput(t, repo, "show", saved+":"+path); got != content {
			t.Fatalf("saved %s = %q, want %q", path, got, content)
		}
	}
	if err := plan2.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := read(t, repo, "note.txt"); got != "edited after the preview\n" {
		t.Fatalf("note.txt after undo = %q, want the latest contents", got)
	}
	if got := read(t, repo, "out"); got != "file where a directory was\n" {
		t.Fatalf("out after undo = %q, want the file back", got)
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
	// The hooks do fire for a plain git command, so none firing below means the
	// snapshot kept them off.
	runGit(t, repo, "update-ref", "refs/test/x", "HEAD")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("hooks did not fire for a plain git command: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
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

	// A committed submodule records only its commit, so an uncommitted edit
	// inside it leaves the snapshot and the worktree looking identical.
	repo = newRepo(t)
	embedded = filepath.Join(repo, "vendor", "lib")
	runGit(t, repo, "init", "-q", embedded)
	write(t, embedded, "lib.go", "package lib\n")
	runGit(t, embedded, "add", ".")
	runGit(t, embedded, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-qm", "lib")
	runGit(t, repo, "add", "vendor/lib")
	runGit(t, repo, "commit", "-qm", "add lib")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, embedded, "lib.go", "package lib // edited by the turn\n")
	if _, err := Plan(context.Background(), repo, ref); !errors.Is(err, ErrNestedRepository) {
		t.Fatalf("Plan(dirty submodule) = %v, want ErrNestedRepository", err)
	}
}

func TestSaveKeepsWhatARestoreOverwrites(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "edited after the turn\n")
	write(t, repo, "new.txt", "created after the turn\n")

	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	saved := BeforeRollbackRef("s1", "turn-1")
	if err := plan.Save(context.Background(), saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := status(t, repo); got != "" {
		t.Fatalf("status after restore = %q, want clean", got)
	}

	runGit(t, repo, "stash", "apply", saved)
	for path, content := range map[string]string{"a.txt": "edited after the turn\n", "new.txt": "created after the turn\n"} {
		if got := read(t, repo, path); got != content {
			t.Fatalf("%s after stash apply = %q, want %q", path, got, content)
		}
	}
}

func TestPlanIDChangesWhenTheFilesDo(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "agent\n")
	planID := func() string {
		t.Helper()
		plan, err := Plan(context.Background(), repo, ref)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		return plan.PlanID
	}

	first := planID()
	if again := planID(); again != first {
		t.Fatalf("PlanID changed with nothing edited: %q then %q", first, again)
	}
	write(t, repo, "c.txt", "edited after the preview\n")
	edited := planID()
	if edited == first {
		t.Fatal("PlanID did not change after a file was created")
	}
	runGit(t, repo, "add", "c.txt")
	if staged := planID(); staged == edited {
		t.Fatal("PlanID did not change after a file was staged")
	}
}

func TestCaptureSkipsTooManyOrTooLargeUntrackedFiles(t *testing.T) {
	for name, create := range map[string]func(t *testing.T, repo string){
		"one large file": func(t *testing.T, repo string) {
			sparse(t, repo, "model.bin", maxUntrackedFileBytes+1)
		},
		"too much in total": func(t *testing.T, repo string) {
			for i := range 5 {
				sparse(t, repo, fmt.Sprintf("data/%d.bin", i), maxUntrackedBytes/4)
			}
		},
		"too many files": func(t *testing.T, repo string) {
			for i := range maxUntrackedFiles + 1 {
				write(t, repo, fmt.Sprintf("gen/%d.txt", i), "x")
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			create(t, repo)
			ref := Ref("s1", "turn-1")
			if err := Capture(context.Background(), repo, ref); !errors.Is(err, ErrTooLarge) {
				t.Fatalf("Capture = %v, want ErrTooLarge", err)
			}
			if _, err := Plan(context.Background(), repo, ref); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Plan after a skipped capture = %v, want ErrNotFound", err)
			}
		})
	}

	// Ignored files never count: they are not snapshotted at all.
	repo := newRepo(t)
	sparse(t, repo, "build/huge.bin", maxUntrackedBytes+1)
	if err := Capture(context.Background(), repo, Ref("s1", "turn-1")); err != nil {
		t.Fatalf("Capture with only ignored output = %v", err)
	}
}

func TestPlanRefusesUntrackedFilesPastTheLimits(t *testing.T) {
	repo := newRepo(t)
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	sparse(t, repo, "model.bin", maxUntrackedFileBytes+1)
	if _, err := Plan(context.Background(), repo, ref); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Plan = %v, want ErrTooLarge", err)
	}
}

func TestRestoreCoversAssumeUnchangedFiles(t *testing.T) {
	repo := newRepo(t)
	runGit(t, repo, "update-index", "--assume-unchanged", "a.txt")
	write(t, repo, "a.txt", "before\n")
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	write(t, repo, "a.txt", "agent\n")
	plan, err := Plan(context.Background(), repo, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if want := []Change{{Path: "a.txt", Kind: ChangeReverted}}; !reflect.DeepEqual(plan.Changes, want) {
		t.Fatalf("Changes = %+v, want %+v", plan.Changes, want)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := read(t, repo, "a.txt"); got != "before\n" {
		t.Fatalf("a.txt = %q, want the pre-turn contents", got)
	}
	if got := gitOutput(t, repo, "ls-files", "-v", "a.txt"); got != "h a.txt" {
		t.Fatalf("real index entry = %q, want it still marked assume-unchanged", got)
	}
}

func TestDeleteOrphansDropsSessionsWhoseWorktreeIsGone(t *testing.T) {
	repo := newRepo(t)
	kept := filepath.Join(t.TempDir(), "kept")
	gone := filepath.Join(t.TempDir(), "gone")
	runGit(t, repo, "worktree", "add", "-b", "kept", kept)
	runGit(t, repo, "worktree", "add", "-b", "gone", gone)
	ctx := context.Background()
	if err := Capture(ctx, kept, Ref("s1", "turn-1")); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := Capture(ctx, gone, Ref("s2", "turn-1"), Ref("s2", "turn-2")); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// A ref that does not say where it was taken is never guessed at.
	runGit(t, repo, "update-ref", Ref("s3", "turn-1"), "HEAD")
	runGit(t, repo, "worktree", "remove", gone)

	if err := DeleteOrphans(ctx, repo, []string{repo, kept}); err != nil {
		t.Fatalf("DeleteOrphans: %v", err)
	}
	want := Ref("s1", "turn-1") + "\n" + Ref("s3", "turn-1")
	if got := gitOutput(t, repo, "for-each-ref", "--format=%(refname)", "refs/ao/snapshots/"); got != want {
		t.Fatalf("snapshot refs left = %q, want %q", got, want)
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

// sparse creates a file of size bytes without writing them.
func sparse(t *testing.T, repo, path string, size int64) {
	t.Helper()
	write(t, repo, path, "")
	if err := os.Truncate(filepath.Join(repo, filepath.FromSlash(path)), size); err != nil {
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
