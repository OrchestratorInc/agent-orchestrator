package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sandboxedWorkspace builds a real-git Workspace whose sessions all count as
// sandboxed (or not), plus one created session worktree.
func sandboxedWorkspace(t *testing.T, sandboxed bool) (*Workspace, ports.WorkspaceInfo, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("planted filter commands use a POSIX shell")
	}
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	opts := Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}}
	if sandboxed {
		opts.SandboxedSession = func(context.Context, ports.WorkspaceInfo) bool { return true }
	}
	ws, err := New(opts)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/nested"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return ws, info, git
}

// plantNestedRepo does what a sandboxed agent can do inside its worktree:
// commit a nested repository as a gitlink whose own config carries a clean
// filter and an fsmonitor command, then leave a modified file in it so any
// host git that recurses into the "submodule" runs both. It returns the
// marker path those commands append to.
func plantNestedRepo(t *testing.T, git, worktree string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "marker")
	sub := filepath.Join(worktree, "sub")
	run(t, git, "init", sub)
	runGit(t, git, sub, "config", "user.email", "agent@example.com")
	runGit(t, git, sub, "config", "user.name", "Agent")
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, sub, "add", "-A")
	runGit(t, git, sub, "commit", "-m", "nested")
	runGit(t, git, sub, "config", "filter.evil.clean", "sh -c 'echo clean >> \""+marker+"\"; cat'")
	runGit(t, git, sub, "config", "core.fsmonitor", "sh -c 'echo fsmonitor >> \""+marker+"\"; exit 1' --")
	runGit(t, git, worktree, "add", "sub")
	runGit(t, git, worktree, "commit", "-m", "add nested repo")
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

func requireNoMarker(t *testing.T, marker string) {
	t.Helper()
	if data, err := os.ReadFile(marker); err == nil {
		t.Fatalf("host git ran a command planted in a nested repository: %q", data)
	}
}

func TestSandboxedDestroyDoesNotRunNestedRepoCommandsAndPreservesWorktree(t *testing.T) {
	ws, info, git := sandboxedWorkspace(t, true)
	marker := plantNestedRepo(t, git, info.Path)

	err := ws.Destroy(context.Background(), info)
	requireNoMarker(t, marker)
	// AO cannot inspect the nested repository without running its config, so
	// a populated gitlink keeps the worktree rather than risking its work.
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "sub", "f.txt")); statErr != nil {
		t.Fatalf("nested repository was not preserved: %v", statErr)
	}
}

func TestSandboxedDestroyRemovesCleanWorktreeWithUnpopulatedGitlink(t *testing.T) {
	ws, info, git := sandboxedWorkspace(t, true)
	plantNestedRepo(t, git, info.Path)
	if err := os.RemoveAll(filepath.Join(info.Path, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(info.Path, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("destroy clean worktree with an uninitialised submodule: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}
}

func TestUnsandboxedDestroyStillReportsDirtySubmodule(t *testing.T) {
	ws, info, git := sandboxedWorkspace(t, false)
	marker := plantNestedRepo(t, git, info.Path)

	// Behaviour for host sessions is unchanged: git's own submodule check
	// still sees the modified nested repository.
	if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy error = %v, want ports.ErrWorkspaceDirty", err)
	}
	// Plain git recursing into the nested repository is what proves the
	// fixture is live; the sandboxed tests rely on it to mean something.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fixture inert: unhardened host git did not run the planted filter: %v", err)
	}
}

func TestSandboxedObserveWorkspaceDoesNotRunNestedRepoCommands(t *testing.T) {
	ws, info, git := sandboxedWorkspace(t, true)
	marker := plantNestedRepo(t, git, info.Path)

	if _, err := ws.ObserveWorkspace(context.Background(), info); err != nil {
		t.Fatalf("observe: %v", err)
	}
	requireNoMarker(t, marker)
}

func TestSandboxedStashAndApplyDoNotRunNestedRepoCommands(t *testing.T) {
	ws, info, git := sandboxedWorkspace(t, true)
	marker := plantNestedRepo(t, git, info.Path)
	if err := os.WriteFile(filepath.Join(info.Path, "wip.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("stash: %v", err)
	}
	requireNoMarker(t, marker)
	if ref == "" {
		t.Fatal("stash returned no ref for a dirty worktree")
	}
	if err := os.Remove(filepath.Join(info.Path, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := ws.ApplyPreserved(ctx, info, ref); err != nil {
		t.Fatalf("apply: %v", err)
	}
	requireNoMarker(t, marker)
}
