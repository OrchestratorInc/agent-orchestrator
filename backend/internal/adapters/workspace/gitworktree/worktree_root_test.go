package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestCreateUnderSessionWorktreeRoot covers the per-project root: a session
// created with an explicit root lands in <root>/<project>/<session>, not under
// the adapter's installation-wide managed root.
func TestCreateUnderSessionWorktreeRoot(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	managed := filepath.Join(tmp, "managed")
	projectRoot := filepath.Join(tmp, "custom root", "工作树")
	if err := os.MkdirAll(projectRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	ws, err := New(Options{Binary: git, ManagedRoot: managed, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/two", WorktreeRoot: projectRoot}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if want := filepath.Join(projectRoot, "proj", "sess"); info.Path != want {
		t.Fatalf("path = %q, want %q", info.Path, want)
	}
	if info.WorktreeRoot == "" {
		t.Fatalf("info.WorktreeRoot is empty, want the creating root %q", projectRoot)
	}
	// The reported root must be the physical one, so a snapshot round-trips
	// through restore validation on platforms where the temp dir is a symlink.
	if physical, evalErr := filepath.EvalSymlinks(projectRoot); evalErr == nil && info.WorktreeRoot != physical {
		t.Fatalf("info.WorktreeRoot = %q, want physical %q", info.WorktreeRoot, physical)
	}

	// Restore with the recorded snapshot re-attaches the same checkout even
	// though the adapter's managed root points elsewhere.
	restored, err := ws.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: cfg.Branch,
		Path: info.Path, WorktreeRoot: info.WorktreeRoot,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Path != info.Path {
		t.Fatalf("restored path = %q, want %q", restored.Path, info.Path)
	}

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}
}

// TestRestoreRejectsPathOutsideRecordedRoot covers the snapshot's reason to
// exist: a recorded worktree is validated against the root it was created
// under, so a project root change cannot move its teardown to another tree.
func TestRestoreRejectsPathOutsideRecordedRoot(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	managed := filepath.Join(tmp, "managed")
	projectRoot := filepath.Join(tmp, "root-a")
	otherRoot := filepath.Join(tmp, "root-b")
	for _, dir := range []string{projectRoot, otherRoot} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := New(Options{Binary: git, ManagedRoot: managed, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/three", WorktreeRoot: projectRoot,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Snapshot says root-a, but the caller claims the worktree lives under
	// root-b: the path is outside the recorded root and must be refused.
	_, err = ws.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/three",
		Path: filepath.Join(otherRoot, "proj", "sess"), WorktreeRoot: projectRoot,
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("restore outside recorded root error = %v, want ErrUnsafePath", err)
	}
	// A snapshot whose directory is gone is recreated like the managed root:
	// first-spawn state and a deleted root look identical here, and the project
	// layer is what refuses a root the user removed (sessionmanager's check).
	if err := os.RemoveAll(projectRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/three",
		Path: info.Path, WorktreeRoot: projectRoot,
	}); err != nil {
		t.Fatalf("restore recreating the recorded root: %v", err)
	}
	if _, err := os.Stat(projectRoot); err != nil {
		t.Fatalf("recorded root was not recreated: %v", err)
	}
	// A snapshot that is a FILE is never a worktree root.
	fileRoot := filepath.Join(tmp, "root-file")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ws.Restore(ctx, ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: "feature/three",
		Path: info.Path, WorktreeRoot: fileRoot,
	})
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("restore with a file as root error = %v, want ErrUnsafePath", err)
	}
}
