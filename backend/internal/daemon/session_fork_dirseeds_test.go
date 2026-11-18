package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/dirseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

func TestDirForkSeedsManagesOnlySessionFolders(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seeds := newDirForkSeeds(dataDir)
	standalone := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	scratch := filepath.Join(dataDir, "worktrees", "scr", "workers", "scr-1")
	// A git project's worktrees: a worker's, and an orchestrator's, which is
	// as deep as a plain folder.
	gitWorker := filepath.Join(dataDir, "worktrees", "app", "app-1")
	gitOrchestrator := filepath.Join(dataDir, "worktrees", "app", "orchestrator", "app-orchestrator")
	home := t.TempDir()
	for _, dir := range []string{standalone, scratch, gitWorker, gitOrchestrator} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "escape")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		standalone:      true,
		scratch:         true,
		gitWorker:       false,
		gitOrchestrator: false,
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

func TestFolderSnapshotStoresOnlyForPlainFolders(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stores := folderSnapshotStores(dataDir)
	standalone := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	withGit := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-2")
	gitOrchestrator := filepath.Join(dataDir, "worktrees", "app", "orchestrator", "app-orchestrator")
	for _, dir := range []string{standalone, withGit, gitOrchestrator} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The agent made the folder a repository of its own; its snapshots go there.
	if err := os.Mkdir(filepath.Join(withGit, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if store, ok := stores(standalone); !ok || store != filepath.Join(dataDir, folderSnapshotsDir, "standalone-1.git") {
		t.Fatalf("stores(standalone) = %q, %v", store, ok)
	}
	for _, path := range []string{withGit, gitOrchestrator, t.TempDir(), filepath.Join(dataDir, "worktrees")} {
		if store, ok := stores(path); ok {
			t.Fatalf("stores(%q) = %q, want none", path, store)
		}
	}
}

type sessionsByID map[domain.SessionID]bool

func (s sessionsByID) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id}, s[id], nil
}

func TestReapFolderSnapshotsKeepsLiveSessionsStores(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, folderSnapshotsDir)
	for _, name := range []string{"standalone-1.git", "standalone-2.git", ".new-store-123"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * forkSeedMaxAge)
	if err := os.Chtimes(filepath.Join(root, ".new-store-123"), past, past); err != nil {
		t.Fatal(err)
	}
	reapFolderSnapshots(context.Background(), dataDir, sessionsByID{"standalone-1": true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := os.Stat(filepath.Join(root, "standalone-1.git")); err != nil {
		t.Fatalf("a live session's store was removed: %v", err)
	}
	for _, name := range []string{"standalone-2.git", ".new-store-123"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", name, err)
		}
	}
}

func TestDirForkSeedsCaptureTurnExportsTheSnapshot(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filesnapshot.SetDetachedStores(folderSnapshotStores(dataDir))
	t.Cleanup(func() { filesnapshot.SetDetachedStores(nil) })
	seeds := newDirForkSeeds(dataDir)
	src := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("turn one"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := filesnapshot.Ref("standalone-1", "turn-2")
	if err := filesnapshot.Capture(context.Background(), src, ref); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("turn two"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := seeds.CaptureTurn(context.Background(), src, ref)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(seed, "a.txt")); err != nil || string(b) != "turn one" {
		t.Fatalf("seed a.txt = %q, %v", b, err)
	}
	if !strings.HasPrefix(seed, filepath.Join(dataDir, dirseed.DirName)+string(filepath.Separator)) {
		t.Fatalf("seed %q is outside the seeds folder", seed)
	}
	if _, err := seeds.CaptureTurn(context.Background(), src, filesnapshot.Ref("standalone-1", "missing")); !errors.Is(err, sessionfork.ErrSnapshotMissing) {
		t.Fatalf("CaptureTurn of a missing snapshot = %v, want ErrSnapshotMissing", err)
	}
}
