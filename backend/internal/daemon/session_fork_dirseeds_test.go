package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
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
	// A folder the agent made a repository keeps its store, which holds the
	// snapshots from before; filesnapshot sends new ones to the repository.
	if err := os.Mkdir(filepath.Join(withGit, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if store, ok := stores(standalone); !ok || store != filepath.Join(dataDir, folderSnapshotsDir, "standalone-1.git") {
		t.Fatalf("stores(standalone) = %q, %v", store, ok)
	}
	if store, ok := stores(withGit); !ok || store != filepath.Join(dataDir, folderSnapshotsDir, "standalone-2.git") {
		t.Fatalf("stores(folder made a repository) = %q, %v", store, ok)
	}
	for _, path := range []string{gitOrchestrator, t.TempDir(), filepath.Join(dataDir, "worktrees")} {
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

// A folder holding a git repository, as itself or inside it, can't have an
// older turn's files written back: its snapshots never recorded the
// repository's own files or history.
func TestDirForkSeedsCaptureTurnRefusesFoldersHoldingARepository(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filesnapshot.SetDetachedStores(folderSnapshotStores(dataDir))
	t.Cleanup(func() { filesnapshot.SetDetachedStores(nil) })
	seeds := newDirForkSeeds(dataDir)
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=T"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	// A repository cloned into the folder.
	nested := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	if err := os.MkdirAll(filepath.Join(nested, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(nested, "init", "-q", "app")
	if err := os.WriteFile(filepath.Join(nested, "app", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(filepath.Join(nested, "app"), "add", ".")
	git(filepath.Join(nested, "app"), "commit", "-qm", "app")
	ref := filesnapshot.Ref("standalone-1", "turn-2")
	if err := filesnapshot.Capture(context.Background(), nested, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := seeds.CaptureTurn(context.Background(), nested, ref); !errors.Is(err, sessionfork.ErrSnapshotHasRepository) {
		t.Fatalf("CaptureTurn(cloned repository) = %v, want ErrSnapshotHasRepository", err)
	}

	// The folder itself made a repository.
	own := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-2")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	git(own, "init", "-q")
	if _, err := seeds.CaptureTurn(context.Background(), own, filesnapshot.Ref("standalone-2", "turn-2")); !errors.Is(err, sessionfork.ErrSnapshotHasRepository) {
		t.Fatalf("CaptureTurn(folder is a repository) = %v, want ErrSnapshotHasRepository", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dataDir, dirseed.DirName)); len(entries) != 0 {
		t.Fatalf("refused captures left seeds: %v", entries)
	}
}

// An agent can make its folder a repository mid-chat. Snapshots from before
// that stay in the folder's store: Roll back still restores them, without
// touching the new repository, and an older-turn fork still starts from them.
// Only snapshots taken after it, kept in the agent's repository without its
// history, refuse an older-turn fork.
func TestFolderSnapshotsFromBeforeGitInitStayUsable(t *testing.T) {
	dataDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filesnapshot.SetDetachedStores(folderSnapshotStores(dataDir))
	t.Cleanup(func() { filesnapshot.SetDetachedStores(nil) })
	seeds := newDirForkSeeds(dataDir)
	folder := filepath.Join(dataDir, "worktrees", "standalone", "sessions", "standalone-1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", folder, "-c", "user.email=t@example.com", "-c", "user.name=T"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(folder, "notes.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	write("turn one\n")
	before := filesnapshot.Ref("standalone-1", "turn-2")
	if err := filesnapshot.Capture(ctx, folder, before); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-qm", "agent's own repository")
	head := git("rev-parse", "HEAD")
	write("turn two\n")
	after := filesnapshot.Ref("standalone-1", "turn-3")
	if err := filesnapshot.Capture(ctx, folder, after); err != nil {
		t.Fatal(err)
	}
	write("turn three\n")

	// An older-turn fork from before the repository starts from that snapshot.
	seed, err := seeds.CaptureTurn(ctx, folder, before)
	if err != nil {
		t.Fatalf("CaptureTurn of a snapshot from before git init = %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(seed, "notes.md")); err != nil || string(b) != "turn one\n" {
		t.Fatalf("seed notes.md = %q, %v", b, err)
	}
	// One from after it would lack the repository's history.
	if _, err := seeds.CaptureTurn(ctx, folder, after); !errors.Is(err, sessionfork.ErrSnapshotHasRepository) {
		t.Fatalf("CaptureTurn of a snapshot from after git init = %v, want ErrSnapshotHasRepository", err)
	}

	// Roll back restores the earlier snapshot's files and leaves the repository alone.
	plan, err := filesnapshot.Plan(ctx, folder, before)
	if err != nil {
		t.Fatalf("Plan of a snapshot from before git init = %v", err)
	}
	if err := plan.Save(ctx, filesnapshot.BeforeRollbackRef("standalone-1", "turn-2")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := plan.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(folder, "notes.md")); err != nil || string(b) != "turn one\n" {
		t.Fatalf("restored notes.md = %q, %v", b, err)
	}
	if got := git("rev-parse", "HEAD"); got != head {
		t.Fatalf("the agent's repository moved: HEAD %s, want %s", got, head)
	}
	if err := plan.Undo(ctx); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(folder, "notes.md")); err != nil || string(b) != "turn three\n" {
		t.Fatalf("notes.md after Undo = %q, %v", b, err)
	}
	// The later snapshot, kept in the agent's repository, still restores too.
	if _, err := filesnapshot.Plan(ctx, folder, after); err != nil {
		t.Fatalf("Plan of a snapshot from after git init = %v", err)
	}
}
