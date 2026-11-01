package sessionmanager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A dirty checkout can replace a tracked folder with an ordinary file: Git then
// lists the untracked file and the deleted children under the old folder, whose
// Lstat fails with ENOTDIR rather than "not exist".
func TestCopyImportedWorkspaceFolderReplacedByFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "ao", "worktree")
	if err := os.MkdirAll(filepath.Join(source, "dir", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(path string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	for _, name := range []string{"dir/child", "dir/nested/deep"} {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte("tracked\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(source, "init", "-b", "main")
	git(source, "add", ".")
	git(source, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "base")
	git(source, "worktree", "add", "-b", "ao-test", destination, "HEAD")
	if err := os.RemoveAll(filepath.Join(source, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "dir"), []byte("now a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := importFacts(ctx, src, []string{"dir", filepath.Join("dir", "child"), filepath.Join("dir", "nested", "deep")})
	_ = src.Close()
	if err != nil {
		t.Fatalf("children of a replaced folder must read as deleted: %v", err)
	}
	if _, ok := facts["dir"]; !ok || len(facts) != 1 {
		t.Fatalf("facts = %v, want only the new file", facts)
	}

	if err := copyImportedWorkspace(ctx, source, destination, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "dir")); err != nil || string(got) != "now a file\n" {
		t.Fatalf("dir = %q, %v; want the file that replaced the folder", got, err)
	}
}

// Only a non-folder ancestor makes a missing child a deletion; a plain folder
// above an unreadable path does not hide the error.
func TestImportAncestorNotDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if !importAncestorNotDir(root, filepath.Join("file", "child", "deeper")) {
		t.Fatal("a file ancestor was not recognized")
	}
	if importAncestorNotDir(root, filepath.Join("folder", "child")) {
		t.Fatal("a folder ancestor was treated as a replaced folder")
	}
	if importAncestorNotDir(root, "top-level") {
		t.Fatal("a top-level path has no ancestor to replace")
	}
}

// A subfolder the new worktree lacks, such as a git-ignored scratch folder,
// falls back to the worktree root rather than a missing working directory.
func TestImportLaunchPathFallsBackWhenSubdirIsMissing(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "pkg", "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := importLaunchPath(ws, filepath.Join("pkg", "api")); got != filepath.Join(ws, "pkg", "api") {
		t.Fatalf("existing subfolder: got %q", got)
	}
	for _, subdir := range []string{filepath.Join("tmp", "scratch"), "file"} {
		if got := importLaunchPath(ws, subdir); got != ws {
			t.Fatalf("subdir %q: got %q, want the worktree root", subdir, got)
		}
	}
}
