package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/dirseed"
)

func TestDirForkSeedsManagesOnlySessionFolders(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seeds := newDirForkSeeds(dataDir)
	standalone := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	scratch := filepath.Join(dataDir, "worktrees", "scr", "workers", "scr-1")
	home := t.TempDir()
	for _, dir := range []string{standalone, scratch} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "escape")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		standalone: true,
		scratch:    true,
		// Everything else is refused: the user's own folders, the managed
		// root and the folders holding several sessions, and a session path
		// that is a link out of the root.
		home:                                false,
		filepath.Join(dataDir, "worktrees"): false,
		filepath.Join(dataDir, "worktrees", "standalone"):             false,
		filepath.Join(dataDir, "worktrees", "standalone", "sessions"): false,
		link: false,
		filepath.Join(dataDir, "worktrees", "standalone", "sessions", "missing"): false,
		"": false,
	} {
		if got := seeds.Manages(path); got != want {
			t.Errorf("Manages(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDirForkSeedsCaptureAndDelete(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seeds := newDirForkSeeds(dataDir)
	src := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := seeds.Capture(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seed, filepath.Join(dataDir, dirseed.DirName)+string(filepath.Separator)) {
		t.Fatalf("seed %q is outside the data folder's seeds", seed)
	}
	if b, err := os.ReadFile(filepath.Join(seed, "a.txt")); err != nil || string(b) != "a" {
		t.Fatalf("seed a.txt = %q, %v", b, err)
	}
	if err := seeds.Delete(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(seed); !os.IsNotExist(err) {
		t.Fatalf("seed kept: %v", err)
	}
	// Delete never removes anything outside the seeds folder.
	if err := seeds.Delete(context.Background(), src); err == nil {
		t.Fatal("Delete removed a folder outside the seeds folder")
	}
	if _, err := os.Stat(filepath.Join(src, "a.txt")); err != nil {
		t.Fatalf("source removed: %v", err)
	}
}

func TestReapDirForkSeedsRemovesLeftovers(t *testing.T) {
	dataDir := t.TempDir()
	old := filepath.Join(dataDir, dirseed.DirName, "left")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * forkSeedMaxAge)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	reapDirForkSeeds(dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("leftover seed kept: %v", err)
	}
}
