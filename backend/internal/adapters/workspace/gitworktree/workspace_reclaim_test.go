package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDestroyReclaimReportsAlreadyAbsentWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-absent", Branch: "feature/absent"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	reclaim, err := ws.DestroyReclaim(ctx, info)
	if err != nil {
		t.Fatalf("destroy reclaim: %v", err)
	}
	if reclaim != ports.WorkspaceReclaimAlreadyAbsent {
		t.Fatalf("reclaim = %q, want %q", reclaim, ports.WorkspaceReclaimAlreadyAbsent)
	}

	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); ok {
		t.Fatalf("worktree %q still registered after DestroyReclaim", info.Path)
	}
}

func TestDestroyReclaimReportsRemovedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-present", Branch: "feature/present"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, statErr := os.Stat(info.Path); statErr != nil {
		t.Fatalf("precondition: worktree missing before destroy: %v", statErr)
	}

	reclaim, err := ws.DestroyReclaim(ctx, info)
	if err != nil {
		t.Fatalf("destroy reclaim: %v", err)
	}
	if reclaim != ports.WorkspaceReclaimRemoved {
		t.Fatalf("reclaim = %q, want %q", reclaim, ports.WorkspaceReclaimRemoved)
	}
	if _, statErr := os.Stat(info.Path); !os.IsNotExist(statErr) {
		t.Fatalf("path after destroy stat err = %v, want not exist", statErr)
	}
}
