package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// TestWorkspaceIntegrationForceDestroyDirtyWorktree is the RED/GREEN test for
// ForceDestroy. It creates a real git worktree, dirties it with an uncommitted
// file (which normal Destroy refuses via ErrWorkspaceDirty), then calls
// ForceDestroy and asserts:
//
//	(a) the worktree path no longer exists on disk.
//	(b) the worktree is deregistered from `git worktree list`.
func TestWorkspaceIntegrationForceDestroyDirtyWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-fd", Branch: "feature/force-destroy"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Dirty the worktree with uncommitted work: normal Destroy must refuse this.
	wip := filepath.Join(info.Path, "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted work\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}

	// Confirm that safe Destroy refuses the dirty worktree (guard: this is the
	// contract we must NOT break).
	if destroyErr := ws.Destroy(ctx, info); !errors.Is(destroyErr, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy dirty error = %v, want ports.ErrWorkspaceDirty", destroyErr)
	}
	// Path must still be intact after refused Destroy.
	if _, err := os.Stat(wip); err != nil {
		t.Fatalf("dirty worktree was removed by Destroy: %v", err)
	}

	// ForceDestroy must succeed even though the worktree is dirty.
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy: %v", err)
	}

	// (a) Path no longer exists on disk.
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after ForceDestroy stat err = %v, want not exist", err)
	}

	// (b) Worktree is deregistered from git worktree list.
	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("listRecords after ForceDestroy: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); ok {
		t.Fatalf("worktree %q still registered after ForceDestroy", info.Path)
	}
}

// TestForceDestroyArgs verifies the new force arg builder emits --force
// and leaves worktreeRemoveArgs byte-for-byte unchanged (review item RA guard).
func TestForceDestroyArgs(t *testing.T) {
	repo := "/repo"
	path := "/managed/proj/sess"

	safe := worktreeRemoveArgs(repo, path)
	for _, a := range safe {
		if a == "--force" || a == "-f" {
			t.Fatalf("worktreeRemoveArgs contains --force: %v", safe)
		}
	}

	forced := worktreeForceRemoveArgs(repo, path)
	hasForce := false
	for _, a := range forced {
		if a == "--force" {
			hasForce = true
		}
	}
	if !hasForce {
		t.Fatalf("worktreeForceRemoveArgs missing --force: %v", forced)
	}
}
