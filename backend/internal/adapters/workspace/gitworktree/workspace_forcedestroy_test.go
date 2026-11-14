package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

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

	wip := filepath.Join(info.Path, "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted work\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}

	if destroyErr := ws.Destroy(ctx, info); !errors.Is(destroyErr, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy dirty error = %v, want ports.ErrWorkspaceDirty", destroyErr)
	}
	if _, err := os.Stat(wip); err != nil {
		t.Fatalf("dirty worktree was removed by Destroy: %v", err)
	}

	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy: %v", err)
	}

	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after ForceDestroy stat err = %v, want not exist", err)
	}

	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("listRecords after ForceDestroy: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); ok {
		t.Fatalf("worktree %q still registered after ForceDestroy", info.Path)
	}
}

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
