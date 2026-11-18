package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/dirseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/scratch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// noGitForkSeeds fails the test if a plain folder reaches any git seed step.
type noGitForkSeeds struct{ t *testing.T }

func (n noGitForkSeeds) fail(step string) {
	n.t.Errorf("git fork seed %s called for a plain folder", step)
}
func (n noGitForkSeeds) Apply(context.Context, string, string) error {
	n.fail("Apply")
	return nil
}
func (n noGitForkSeeds) Delete(context.Context, string, string) error {
	n.fail("Delete")
	return nil
}
func (n noGitForkSeeds) RecordBase(context.Context, string, string) error {
	n.fail("RecordBase")
	return nil
}
func (n noGitForkSeeds) UnchangedSince(context.Context, string, string) (bool, error) {
	n.fail("UnchangedSince")
	return true, nil
}
func (n noGitForkSeeds) RestoreBase(context.Context, string, string) (bool, error) {
	n.fail("RestoreBase")
	return true, nil
}
func (n noGitForkSeeds) DiscardChanges(context.Context, string) error {
	n.fail("DiscardChanges")
	return nil
}

// standaloneForkManager is a manager whose workspaces are the scratch
// adapter's plain folders under a temp root.
func standaloneForkManager(t *testing.T) (*Manager, *fakeStore, string) {
	t.Helper()
	m, st, _, _ := newManager()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := scratch.New(scratch.Options{ManagedRoot: filepath.Join(root, "worktrees")})
	if err != nil {
		t.Fatal(err)
	}
	m.workspace = adapter
	m.forkSeeds = noGitForkSeeds{t: t}
	m.dataDir = root
	return m, st, root
}

func standaloneSourceFolder(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join(root, "worktrees", "standalone", "sessions", "standalone-1")
	writeTestFile(t, filepath.Join(src, "draft.md"), "draft")
	writeTestFile(t, filepath.Join(src, "data", "rows.csv"), "a,b")
	return src
}

func TestStandaloneForkWorkspaceGetsSourceFiles(t *testing.T) {
	m, _, root := standaloneForkManager(t)
	src := standaloneSourceFolder(t, root)
	seed := filepath.Join(root, dirseed.DirName, "seed-1")
	if err := dirseed.Capture(context.Background(), src, seed); err != nil {
		t.Fatal(err)
	}
	cfg := ports.SpawnConfig{Kind: domain.KindWorker, Fork: &ports.SpawnFork{SourceSessionID: "standalone-1", SeedDir: seed, SourceWorkspace: src}}
	ws, _, err := m.createSessionWorkspace(context.Background(), domain.ProjectRecord{}, cfg, "standalone-2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "worktrees", "standalone", "sessions", "standalone-2"); ws.Path != want {
		t.Fatalf("fork workspace = %q, want %q", ws.Path, want)
	}
	for rel, want := range map[string]string{"draft.md": "draft", "data/rows.csv": "a,b"} {
		if b, err := os.ReadFile(filepath.Join(ws.Path, rel)); err != nil || string(b) != want {
			t.Fatalf("fork %s = %q, %v; want %q", rel, b, err, want)
		}
		if b, err := os.ReadFile(filepath.Join(src, rel)); err != nil || string(b) != want {
			t.Fatalf("source %s = %q, %v; want it unchanged", rel, b, err)
		}
	}
	if _, err := os.Lstat(seed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed left behind: %v", err)
	}
	if !m.takeForkWorkspace(ws.Path) {
		t.Fatal("the fork's setup should run as a fork's")
	}
}

// An older turn's files come without dependency folders, which snapshots
// leave out; the fork clones them from its original, as a git fork does.
func TestStandaloneOlderTurnForkGetsTheOriginalsDependencies(t *testing.T) {
	m, _, root := standaloneForkManager(t)
	src := standaloneSourceFolder(t, root)
	writeTestFile(t, filepath.Join(src, "node_modules", "left-pad", "index.js"), "pad")
	seed := filepath.Join(root, dirseed.DirName, "turn-seed")
	writeTestFile(t, filepath.Join(seed, "draft.md"), "older draft")
	cfg := ports.SpawnConfig{Kind: domain.KindWorker, Fork: &ports.SpawnFork{SourceSessionID: "standalone-1", SeedDir: seed, SourceWorkspace: src}}
	ws, _, err := m.createSessionWorkspace(context.Background(), domain.ProjectRecord{}, cfg, "standalone-2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(ws.Path, "draft.md")); err != nil || string(b) != "older draft" {
		t.Fatalf("draft.md = %q, %v; want the older turn's", b, err)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "data", "rows.csv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the fork got the original's current files: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(ws.Path, "node_modules", "left-pad", "index.js")); err != nil || string(b) != "pad" {
		t.Fatalf("node_modules not cloned: %q, %v", b, err)
	}
}

// A seed that can't be applied fails the spawn, and the created folder is
// returned empty so the caller's rollback removes it.
func TestStandaloneForkApplyFailureReturnsWorkspace(t *testing.T) {
	m, _, root := standaloneForkManager(t)
	// A seed holding a file and a dangling entry the move can't take: the
	// file may arrive before the failure.
	seed := filepath.Join(root, dirseed.DirName, "seed-1")
	writeTestFile(t, filepath.Join(seed, "a.txt"), "a")
	if err := os.Chmod(seed, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(seed, 0o700) })
	cfg := ports.SpawnConfig{Kind: domain.KindWorker, Fork: &ports.SpawnFork{SourceSessionID: "standalone-1", SeedDir: seed}}
	ws, _, err := m.createSessionWorkspace(context.Background(), domain.ProjectRecord{}, cfg, "standalone-2", "", nil)
	if err == nil {
		t.Fatal("want an error when the seed can't be applied")
	}
	if ws.Path == "" {
		t.Fatal("the created folder must be returned for rollback")
	}
	if entries, err := os.ReadDir(ws.Path); err != nil || len(entries) != 0 {
		t.Fatalf("failed fork's folder holds %v (%v), want it empty", entries, err)
	}
	if err := m.workspace.Destroy(context.Background(), ws); err != nil {
		t.Fatalf("rollback could not remove the folder: %v", err)
	}
}

func standaloneForkRecords(root string) (fork, source domain.SessionRecord) {
	source = domain.SessionRecord{ID: "standalone-1", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(root, "worktrees", "standalone", "sessions", "standalone-1")}}
	fork = domain.SessionRecord{ID: "standalone-2", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(root, "worktrees", "standalone", "sessions", "standalone-2")}}
	return fork, source
}

func TestStandaloneForkRestoreLeavesFilesAlone(t *testing.T) {
	m, st, root := standaloneForkManager(t)
	fork, source := standaloneForkRecords(root)
	st.sessions[source.ID] = source
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{fork.ID: {ForkSessionID: fork.ID, SourceSessionID: source.ID}}}
	// The folder is gone, so restore recreates it; a git fork would get its
	// starting files back here.
	if _, err := m.restoreSessionWorkspace(context.Background(), domain.ProjectRecord{}, fork); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fork.Metadata.WorkspacePath); err != nil {
		t.Fatalf("restore did not recreate the folder: %v", err)
	}
}

func TestStandaloneForkArchiveKeepsFolder(t *testing.T) {
	m, st, root := standaloneForkManager(t)
	fork, source := standaloneForkRecords(root)
	writeTestFile(t, filepath.Join(fork.Metadata.WorkspacePath, "draft.md"), "draft")
	st.sessions[source.ID] = source
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{fork.ID: {ForkSessionID: fork.ID, SourceSessionID: source.ID}}}
	if m.dropInheritedForkChanges(context.Background(), fork, workspaceInfo(fork)) {
		t.Fatal("a plain folder's files were discarded as inherited changes")
	}
	if _, err := os.Stat(filepath.Join(fork.Metadata.WorkspacePath, "draft.md")); err != nil {
		t.Fatal(err)
	}
}

func TestScratchProjectForkArchiveSkipsGit(t *testing.T) {
	m, st, root := standaloneForkManager(t)
	st.projects["scr"] = domain.ProjectRecord{ID: "scr", Kind: domain.ProjectKindScratch, Path: t.TempDir()}
	fork, source := standaloneForkRecords(root)
	fork.ProjectID, source.ProjectID = "scr", "scr"
	st.sessions[source.ID] = source
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{fork.ID: {ForkSessionID: fork.ID, SourceSessionID: source.ID}}}
	if m.dropInheritedForkChanges(context.Background(), fork, workspaceInfo(fork)) {
		t.Fatal("a Scratch project's folder was discarded as inherited changes")
	}
}
