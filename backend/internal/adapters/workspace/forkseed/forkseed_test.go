package forkseed

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sourceRepo has a pushed-nowhere commit, an edit, a staged file, a new file,
// a deleted file, and an ignored file.
func sourceRepo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "a1\n")
	write(t, filepath.Join(dir, "gone.txt"), "x\n")
	write(t, filepath.Join(dir, ".gitignore"), "ignored/\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "one")
	write(t, filepath.Join(dir, "a.txt"), "a2\n")
	git(t, dir, "commit", "-qam", "two (unpushed)")
	write(t, filepath.Join(dir, "a.txt"), "a3 uncommitted\n")
	write(t, filepath.Join(dir, "staged.txt"), "staged\n")
	git(t, dir, "add", "staged.txt")
	write(t, filepath.Join(dir, "new", "untracked.txt"), "new\n")
	write(t, filepath.Join(dir, "ignored", "big.bin"), "ignored\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCaptureReturnsHeadAndLeavesSourceUntouched(t *testing.T) {
	src := sourceRepo(t)
	status := git(t, src, "status", "--porcelain")
	stash := git(t, src, "stash", "list")
	head := git(t, src, "rev-parse", "HEAD")
	got, err := Capture(context.Background(), src, "refs/ao/fork-seeds/t1")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Fatalf("head = %q, want %q", got, head)
	}
	if s := git(t, src, "status", "--porcelain"); s != status {
		t.Fatalf("status changed:\n%s\nwant\n%s", s, status)
	}
	if s := git(t, src, "stash", "list"); s != stash {
		t.Fatalf("stash changed: %q", s)
	}
	if b := git(t, src, "branch", "--show-current"); b != "main" {
		t.Fatalf("branch = %q", b)
	}
}

func TestApplyCopiesUncommittedAndUntracked(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := "refs/ao/fork-seeds/t2"
	head, err := Capture(ctx, src, ref)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "fork")
	git(t, src, "worktree", "add", "-q", "-b", "fork", dst, head)
	if err := Apply(ctx, dst, ref); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "a.txt")); got != "a3 uncommitted\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := read(t, filepath.Join(dst, "staged.txt")); got != "staged\n" {
		t.Fatalf("staged.txt = %q", got)
	}
	if got := read(t, filepath.Join(dst, "new", "untracked.txt")); got != "new\n" {
		t.Fatalf("untracked = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "gone.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gone.txt should be deleted, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "ignored")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ignored files must not be copied, err=%v", err)
	}
	if got := git(t, dst, "rev-parse", "HEAD"); got != head {
		t.Fatalf("fork HEAD = %q, want %q", got, head)
	}
	// The changes stay uncommitted in the fork.
	if s := git(t, dst, "status", "--porcelain"); !strings.Contains(s, "a.txt") || !strings.Contains(s, "new/") {
		t.Fatalf("fork status = %q", s)
	}
}

func TestDeleteRemovesRef(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := "refs/ao/fork-seeds/t3"
	if _, err := Capture(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	if err := Delete(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	if out := git(t, src, "for-each-ref", "refs/ao/fork-seeds/"); out != "" {
		t.Fatalf("refs left: %q", out)
	}
	if err := Delete(ctx, src, ref); err != nil {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestListAndCheckOutsideRepo(t *testing.T) {
	ctx := context.Background()
	if _, err := Capture(ctx, t.TempDir(), ""); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("err = %v, want ErrNotRepository", err)
	}
	src := sourceRepo(t)
	if _, err := Capture(ctx, src, "refs/ao/fork-seeds/a"); err != nil {
		t.Fatal(err)
	}
	refs, err := List(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "refs/ao/fork-seeds/a" {
		t.Fatalf("refs = %v", refs)
	}
}

func TestReapRemovesOrphanSeedRefs(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	if _, err := Capture(ctx, src, RefPrefix+"old"); err != nil {
		t.Fatal(err)
	}
	removed, err := Reap(ctx, src, time.Hour, time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	if _, err := Capture(ctx, src, RefPrefix+"fresh"); err != nil {
		t.Fatal(err)
	}
	if removed, err := Reap(ctx, src, time.Hour, time.Now()); err != nil || removed != 0 {
		t.Fatalf("a fresh seed was reaped: %d, %v", removed, err)
	}
}

func TestDeleteNeverTouchesOtherRefs(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	git(t, src, "update-ref", "refs/ao/snapshots/s/turn-1", "HEAD")
	if err := Delete(ctx, src, "refs/ao/snapshots/s/turn-1"); err == nil {
		t.Fatal("deleting a ref outside the fork-seed namespace must be refused")
	}
	if out := git(t, src, "for-each-ref", "refs/ao/snapshots/"); out == "" {
		t.Fatal("the rollback snapshot was deleted")
	}
}

func TestSnapshotHeadIsTheSnapshotsBase(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	head := git(t, src, "rev-parse", "HEAD")
	ref := RefPrefix + "snap"
	if _, err := Capture(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	got, err := SnapshotHead(ctx, src, ref)
	if err != nil || got != head {
		t.Fatalf("head = %q, %v; want %q", got, err, head)
	}
	if _, err := SnapshotHead(ctx, src, "refs/ao/snapshots/none"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnchangedSinceBase(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := BaseRef("fork-1")
	if err := RecordBase(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	same, err := UnchangedSince(ctx, src, ref)
	if err != nil || !same {
		t.Fatalf("right after recording: same=%v err=%v", same, err)
	}
	write(t, filepath.Join(src, "new", "untracked.txt"), "changed in the fork\n")
	if same, _ := UnchangedSince(ctx, src, ref); same {
		t.Fatal("an edit after the fork must count as a change")
	}
	if same, err := UnchangedSince(ctx, src, BaseRef("missing")); err != nil || same {
		t.Fatalf("no base recorded: same=%v err=%v", same, err)
	}
	if err := Delete(ctx, src, ref); err != nil {
		t.Fatalf("a base ref is the fork's own and can be deleted: %v", err)
	}
}

func TestDiscardInheritedLeavesTheWorktreeClean(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	if err := DiscardChanges(ctx, src); err != nil {
		t.Fatal(err)
	}
	if s := git(t, src, "status", "--porcelain"); s != "" {
		t.Fatalf("status = %q", s)
	}
	if _, err := os.Stat(filepath.Join(src, "ignored", "big.bin")); err != nil {
		t.Fatal("ignored files such as dependencies are left alone")
	}
}

// A fork's worktree recreated on restore gets its starting files back,
// uncommitted, as long as its branch hasn't moved since the fork.
func TestRestoreBasePutsTheForksStartingFilesBack(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := BaseRef("fork-1")
	if err := RecordBase(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	// A recreated worktree: clean, at the commit the fork started on.
	restored := filepath.Join(t.TempDir(), "restored")
	git(t, src, "worktree", "add", "-q", "--detach", restored, "HEAD")

	applied, err := RestoreBase(ctx, restored, ref)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if got := read(t, filepath.Join(restored, "a.txt")); got != "a3 uncommitted\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := read(t, filepath.Join(restored, "new", "untracked.txt")); got != "new\n" {
		t.Fatalf("untracked file = %q", got)
	}
	if git(t, restored, "rev-parse", "HEAD") != git(t, src, "rev-parse", "HEAD") {
		t.Fatal("restoring the files must not move HEAD")
	}
}

// A fork that committed since it started keeps its own history: its starting
// files would undo those commits, so they aren't written back.
func TestRestoreBaseLeavesAForkThatMovedOn(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := BaseRef("fork-1")
	if err := RecordBase(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-qm", "the fork's own work")
	write(t, filepath.Join(src, "a.txt"), "after the fork\n")
	git(t, src, "commit", "-qam", "more")

	applied, err := RestoreBase(ctx, src, ref)
	if err != nil || applied {
		t.Fatalf("applied=%v err=%v; a fork that moved on must be left alone", applied, err)
	}
	if got := read(t, filepath.Join(src, "a.txt")); got != "after the fork\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if applied, err := RestoreBase(ctx, src, BaseRef("missing")); err != nil || applied {
		t.Fatalf("no base recorded: applied=%v err=%v", applied, err)
	}
}

// A recreated worktree that already got the fork's own edits back, from the
// copy archiving preserved, keeps them: the starting files would overwrite
// them.
func TestRestoreBaseLeavesReplayedEditsAlone(t *testing.T) {
	src := sourceRepo(t)
	ctx := context.Background()
	ref := BaseRef("fork-1")
	if err := RecordBase(ctx, src, ref); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	git(t, src, "worktree", "add", "-q", "--detach", restored, "HEAD")
	write(t, filepath.Join(restored, "a.txt"), "the fork's own edit\n")

	applied, err := RestoreBase(ctx, restored, ref)
	if err != nil || applied {
		t.Fatalf("applied=%v err=%v; replayed edits must be left alone", applied, err)
	}
	if got := read(t, filepath.Join(restored, "a.txt")); got != "the fork's own edit\n" {
		t.Fatalf("a.txt = %q", got)
	}
}
