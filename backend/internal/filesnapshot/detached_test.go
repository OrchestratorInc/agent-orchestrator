package filesnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// plainFolder makes a folder with no git repository and routes its snapshots
// to a store outside it, as the daemon does for a standalone agent's folder.
func plainFolder(t *testing.T) (folder, store string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder, store = filepath.Join(root, "folder"), filepath.Join(root, "stores", "s1.git")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	SetDetachedStores(func(workspace string) (string, bool) {
		if filepath.Clean(workspace) == folder {
			return store, true
		}
		return "", false
	})
	t.Cleanup(func() { SetDetachedStores(nil) })
	return folder, store
}

func TestDetachedSnapshotRestoresAPlainFolder(t *testing.T) {
	folder, store := plainFolder(t)
	write(t, folder, "notes.md", "hello\n")
	write(t, folder, "data/rows.csv", "a,b\n")
	write(t, folder, "node_modules/dep/index.js", "dep\n")
	ref := Ref("s1", "turn-2")
	if err := Capture(context.Background(), folder, ref); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(folder, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a snapshot wrote .git into the folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, "HEAD")); err != nil {
		t.Fatalf("store not created: %v", err)
	}

	// The turn edits, deletes, creates, and changes a dependency.
	write(t, folder, "notes.md", "changed\n")
	remove(t, folder, "data/rows.csv")
	write(t, folder, "new.txt", "new\n")
	write(t, folder, "node_modules/dep/index.js", "upgraded\n")

	plan, err := Plan(context.Background(), folder, ref)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := map[string]ChangeKind{"notes.md": ChangeReverted, "data/rows.csv": ChangeRestored, "new.txt": ChangeRemoved}
	if len(plan.Changes) != len(want) {
		t.Fatalf("changes = %+v, want %v", plan.Changes, want)
	}
	for _, change := range plan.Changes {
		if want[change.Path] != change.Kind {
			t.Fatalf("change %+v, want %v", change, want)
		}
	}
	if plan.CommitsSince != 0 {
		t.Fatalf("commits since = %d, want 0", plan.CommitsSince)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := read(t, folder, "notes.md"); got != "hello\n" {
		t.Fatalf("notes.md = %q", got)
	}
	if got := read(t, folder, "data/rows.csv"); got != "a,b\n" {
		t.Fatalf("rows.csv = %q", got)
	}
	if _, err := os.Stat(filepath.Join(folder, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new.txt kept: %v", err)
	}
	// Dependency folders are left out, as a git project ignores them.
	if got := read(t, folder, "node_modules/dep/index.js"); got != "upgraded\n" {
		t.Fatalf("a restore touched node_modules: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(folder, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a restore wrote .git into the folder: %v", err)
	}
}

func TestDetachedUndoPutsBackWhatApplyReplaced(t *testing.T) {
	folder, _ := plainFolder(t)
	write(t, folder, "a.txt", "before\n")
	ref := Ref("s1", "turn-2")
	if err := Capture(context.Background(), folder, ref); err != nil {
		t.Fatal(err)
	}
	write(t, folder, "a.txt", "after\n")
	plan, err := Plan(context.Background(), folder, ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Save(context.Background(), BeforeRollbackRef("s1", "turn-2")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := plan.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if got := read(t, folder, "a.txt"); got != "after\n" {
		t.Fatalf("a.txt = %q after Undo", got)
	}
}

func TestDetachedPlanWithoutSnapshot(t *testing.T) {
	folder, _ := plainFolder(t)
	write(t, folder, "a.txt", "a\n")
	if _, err := Plan(context.Background(), folder, Ref("s1", "missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Plan = %v, want ErrNotFound", err)
	}
}

func TestDetachedCaptureRefusesTooManyFiles(t *testing.T) {
	folder, _ := plainFolder(t)
	sparse(t, folder, "huge.bin", maxUntrackedFileBytes+1)
	if err := Capture(context.Background(), folder, Ref("s1", "turn-2")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Capture = %v, want ErrTooLarge", err)
	}
}

func TestExportWritesATurnsFilesIntoANewFolder(t *testing.T) {
	folder, _ := plainFolder(t)
	write(t, folder, "notes.md", "turn one\n")
	write(t, folder, "deep/x.txt", "x\n")
	write(t, folder, "node_modules/dep/index.js", "dep\n")
	if err := os.Symlink("notes.md", filepath.Join(folder, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ref := Ref("s1", "turn-2")
	if err := Capture(context.Background(), folder, ref); err != nil {
		t.Fatal(err)
	}
	write(t, folder, "notes.md", "turn two\n")

	dst := filepath.Join(t.TempDir(), "seed")
	if err := Export(context.Background(), folder, ref, dst); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := read(t, dst, "notes.md"); got != "turn one\n" {
		t.Fatalf("exported notes.md = %q", got)
	}
	if got := read(t, dst, "deep/x.txt"); got != "x\n" {
		t.Fatalf("exported x.txt = %q", got)
	}
	if target, err := os.Readlink(filepath.Join(dst, "link")); err != nil || target != "notes.md" {
		t.Fatalf("link -> %q, %v", target, err)
	}
	if info, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh = %v, %v; want executable", info, err)
	}
	for _, name := range []string{"node_modules", ".git"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("export wrote %s: %v", name, err)
		}
	}
	if got := read(t, folder, "notes.md"); got != "turn two\n" {
		t.Fatalf("export changed the source: %q", got)
	}
}

func TestExportOfAMissingSnapshot(t *testing.T) {
	folder, _ := plainFolder(t)
	dst := filepath.Join(t.TempDir(), "seed")
	if err := Export(context.Background(), folder, Ref("s1", "missing"), dst); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Export = %v, want ErrNotFound", err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed export left %s behind: %v", dst, err)
	}
}

// Export reads a git project's snapshots too.
func TestExportFromAGitWorktree(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "notes.txt", "untracked\n")
	ref := Ref("s1", "turn-1")
	if err := Capture(context.Background(), repo, ref); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "seed")
	if err := Export(context.Background(), repo, ref, dst); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := read(t, dst, "notes.txt"); got != "untracked\n" {
		t.Fatalf("notes.txt = %q", got)
	}
}

func TestFolderWithoutStoreIsNotARepository(t *testing.T) {
	plainFolder(t)
	other := t.TempDir()
	if err := Capture(context.Background(), other, Ref("s1", "turn-2")); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Capture = %v, want ErrNotRepository", err)
	}
}

func TestDetachedStoreCreatedOnceUnderConcurrentTurns(t *testing.T) {
	folder, _ := plainFolder(t)
	write(t, folder, "a.txt", "a\n")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = Capture(context.Background(), folder, Ref("s1", "turn-"+string(rune('a'+i))))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
	}
	for i := range errs {
		if _, err := Plan(context.Background(), folder, Ref("s1", "turn-"+string(rune('a'+i)))); err != nil {
			t.Fatalf("snapshot %d lost: %v", i, err)
		}
	}
}
