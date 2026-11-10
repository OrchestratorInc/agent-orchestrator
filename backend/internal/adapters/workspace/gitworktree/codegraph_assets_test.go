package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestWorkspaceProjectSkipsCodegraphAssets 覆盖旧登记记录与资料目录内的索引，防止读取检索进程持有的数据库。
func TestWorkspaceProjectSkipsCodegraphAssets(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	root := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	for _, rel := range []string{".codegraph", "notes/.codegraph"} {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "codegraph.db-shm"), []byte("search index state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "context.txt"), []byte("project context"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": root}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/codegraph-assets",
		RootRepoPath: root,
		Assets: []ports.WorkspaceProjectAssetConfig{
			{RelativePath: ".codegraph", SourcePath: filepath.Join(root, ".codegraph")},
			{RelativePath: "notes", SourcePath: filepath.Join(root, "notes")},
		},
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, rel := range []string{".codegraph", "notes/.codegraph"} {
		if _, err := os.Lstat(filepath.Join(info.Root.Path, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("CodeGraph directory %q was copied: %v", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel), "codegraph.db-shm"))
		if err != nil || string(got) != "search index state" {
			t.Errorf("source CodeGraph state %q changed: %q, %v", rel, got, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(info.Root.Path, "notes", "context.txt")); err != nil || string(got) != "project context" {
		t.Fatalf("copied notes = %q, %v", got, err)
	}
}
