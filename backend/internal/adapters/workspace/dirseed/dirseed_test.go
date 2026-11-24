package dirseed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bothPaths runs a test once with the platform's clone and once with the
// plain copy every other platform falls back to.
func bothPaths(t *testing.T, test func(t *testing.T)) {
	t.Run("clone", test)
	t.Run("copy", func(t *testing.T) {
		saved := cloneTree
		cloneTree = func(string, string) error { return ErrCloneUnsupported }
		t.Cleanup(func() { cloneTree = saved })
		test(t)
	})
}

func sourceFolder(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "notes.md"), "notes", 0o644)
	writeFile(t, filepath.Join(src, "src", "deep", "main.go"), "package main", 0o644)
	writeFile(t, filepath.Join(src, "node_modules", "left-pad", "index.js"), "pad", 0o644)
	writeFile(t, filepath.Join(src, ".env"), "KEY=1", 0o600)
	writeFile(t, filepath.Join(src, "run.sh"), "#!/bin/sh", 0o755)
	return src
}

func TestCaptureCopiesFilesAndLeavesSourceUntouched(t *testing.T) {
	bothPaths(t, func(t *testing.T) {
		src := sourceFolder(t)
		seed := filepath.Join(t.TempDir(), DirName, "seed-1")
		if err := Capture(context.Background(), src, seed); err != nil {
			t.Fatal(err)
		}
		for rel, want := range map[string]string{
			"notes.md": "notes", "src/deep/main.go": "package main", ".env": "KEY=1",
		} {
			if got := readFile(t, filepath.Join(seed, rel)); got != want {
				t.Errorf("%s = %q, want %q", rel, got, want)
			}
			if got := readFile(t, filepath.Join(src, rel)); got != want {
				t.Errorf("source %s changed to %q", rel, got)
			}
		}
		info, err := os.Stat(filepath.Join(seed, "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("run.sh lost its executable bit: %v", info.Mode())
		}
		// Changing the seed never reaches the source.
		writeFile(t, filepath.Join(seed, "notes.md"), "changed", 0o644)
		if got := readFile(t, filepath.Join(src, "notes.md")); got != "notes" {
			t.Errorf("source notes.md = %q after editing the seed", got)
		}
	})
}

func TestCapturePreservesSymlinksAsLinks(t *testing.T) {
	bothPaths(t, func(t *testing.T) {
		src := sourceFolder(t)
		outside := filepath.Join(t.TempDir(), "secret")
		writeFile(t, outside, "private key", 0o600)
		if err := os.Symlink(outside, filepath.Join(src, "key")); err != nil {
			t.Fatal(err)
		}
		seed := filepath.Join(t.TempDir(), "seed")
		if err := Capture(context.Background(), src, seed); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(filepath.Join(seed, "key"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("key is %v in the seed, want a symlink", info.Mode())
		}
		if target, err := os.Readlink(filepath.Join(seed, "key")); err != nil || target != outside {
			t.Fatalf("key -> %q (%v), want %q", target, err, outside)
		}
	})
}

func TestCaptureCancelledRemovesPartialSeed(t *testing.T) {
	saved := cloneTree
	cloneTree = func(string, string) error { return ErrCloneUnsupported }
	t.Cleanup(func() { cloneTree = saved })
	src := sourceFolder(t)
	seed := filepath.Join(t.TempDir(), "seed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Capture(ctx, src, seed); !errors.Is(err, context.Canceled) {
		t.Fatalf("Capture = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(seed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial seed left behind: %v", err)
	}
	if got := readFile(t, filepath.Join(src, "notes.md")); got != "notes" {
		t.Fatalf("source changed: %q", got)
	}
}

func TestCaptureRefusesExistingSeed(t *testing.T) {
	src := sourceFolder(t)
	seed := t.TempDir()
	writeFile(t, filepath.Join(seed, "keep"), "keep", 0o644)
	if err := Capture(context.Background(), src, seed); err == nil {
		t.Fatal("Capture into an existing folder succeeded")
	}
	if got := readFile(t, filepath.Join(seed, "keep")); got != "keep" {
		t.Fatalf("existing folder changed: %q", got)
	}
}

func TestCaptureRefusesMissingWorkspace(t *testing.T) {
	seed := filepath.Join(t.TempDir(), "seed")
	if err := Capture(context.Background(), filepath.Join(t.TempDir(), "gone"), seed); err == nil {
		t.Fatal("Capture of a missing folder succeeded")
	}
	if _, err := os.Lstat(seed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed created for a missing folder: %v", err)
	}
}

func TestApplyMovesSeedIntoEmptyWorkspace(t *testing.T) {
	src := sourceFolder(t)
	seed := filepath.Join(t.TempDir(), "seed")
	if err := Capture(context.Background(), src, seed); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := Apply(context.Background(), seed, workspace); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(workspace, "src", "deep", "main.go")); got != "package main" {
		t.Fatalf("main.go = %q", got)
	}
	if _, err := os.Lstat(seed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed kept after Apply: %v", err)
	}
}

func TestApplyRefusesNonEmptyWorkspace(t *testing.T) {
	src := sourceFolder(t)
	seed := filepath.Join(t.TempDir(), "seed")
	if err := Capture(context.Background(), src, seed); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "mine.txt"), "mine", 0o644)
	if err := Apply(context.Background(), seed, workspace); err == nil {
		t.Fatal("Apply into a non-empty folder succeeded")
	}
	if got := readFile(t, filepath.Join(workspace, "mine.txt")); got != "mine" {
		t.Fatalf("existing file changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed files written into a non-empty folder: %v", err)
	}
}

func TestDeleteRemovesSeedAndToleratesMissing(t *testing.T) {
	seed := filepath.Join(t.TempDir(), "seed")
	writeFile(t, filepath.Join(seed, "a"), "a", 0o644)
	if err := Delete(seed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(seed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed still there: %v", err)
	}
	if err := Delete(seed); err != nil {
		t.Fatalf("Delete of a missing seed = %v", err)
	}
	if err := Delete(""); err == nil {
		t.Fatal("Delete of an empty path succeeded")
	}
}

func TestReapRemovesOnlyOldSeeds(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old, fresh := filepath.Join(root, "old"), filepath.Join(root, "fresh")
	writeFile(t, filepath.Join(old, "a"), "a", 0o644)
	writeFile(t, filepath.Join(fresh, "a"), "a", 0o644)
	if err := os.Chtimes(old, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	removed, err := Reap(root, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old seed kept: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh seed removed: %v", err)
	}
	if n, err := Reap(filepath.Join(root, "missing"), time.Hour, now); err != nil || n != 0 {
		t.Fatalf("Reap of a missing root = %d, %v", n, err)
	}
}

// A clone keeps the source folder's times; a seed captured just now from a
// folder last changed long ago must not look like a leftover.
func TestReapKeepsFreshCaptureOfOldFolder(t *testing.T) {
	bothPaths(t, func(t *testing.T) {
		src := sourceFolder(t)
		long := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(src, long, long); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		seed := filepath.Join(root, "seed")
		if err := Capture(context.Background(), src, seed); err != nil {
			t.Fatal(err)
		}
		if removed, err := Reap(root, time.Hour, time.Now()); err != nil || removed != 0 {
			t.Fatalf("Reap removed %d (%v) seeds just captured", removed, err)
		}
	})
}

// A clone costs nothing whatever the folder holds, so it keeps the dependency
// folders. The plain copy, on disks without clones, would read every byte of
// them inside the fork request, so it leaves them out at any depth, as a turn's
// snapshot does.
func TestCaptureCopyLeavesDependencyFoldersOut(t *testing.T) {
	src := sourceFolder(t)
	writeFile(t, filepath.Join(src, "pkg", "app", "node_modules", "react", "index.js"), "react", 0o644)
	writeFile(t, filepath.Join(src, ".venv", "bin", "python"), "py", 0o755)
	writeFile(t, filepath.Join(src, "my_node_modules_notes.md"), "kept", 0o644)
	deps := []string{"node_modules/left-pad/index.js", "pkg/app/node_modules/react/index.js", ".venv/bin/python"}

	saved := cloneTree
	cloneTree = func(string, string) error { return ErrCloneUnsupported }
	copied := filepath.Join(t.TempDir(), "copied")
	err := Capture(context.Background(), src, copied)
	cloneTree = saved
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range deps {
		if _, err := os.Lstat(filepath.Join(copied, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the copy holds %s: %v", rel, err)
		}
	}
	if got := readFile(t, filepath.Join(copied, "my_node_modules_notes.md")); got != "kept" {
		t.Errorf("a file merely named like a dependency folder was dropped: %q", got)
	}

	cloned := filepath.Join(t.TempDir(), "cloned")
	if err := Capture(context.Background(), src, cloned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(cloned, deps[0])); errors.Is(err, os.ErrNotExist) {
		if errors.Is(CloneTree(src, filepath.Join(t.TempDir(), "probe")), ErrCloneUnsupported) {
			t.Skip("no copy-on-write clones here")
		}
		t.Errorf("the clone left %s out", deps[0])
	}
}
