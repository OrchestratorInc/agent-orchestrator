package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestAddWorkspaceExcludesCodegraph 防止代码检索索引被登记为资产或由根仓库初始化提交。
func TestAddWorkspaceExcludesCodegraph(t *testing.T) {
	configureCommitter(t)
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent := t.TempDir()
	gitRepoWithCommit(t, filepath.Join(parent, "api"))
	for _, dir := range []string{".codegraph", "notes"} {
		if err := os.Mkdir(filepath.Join(parent, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, ".codegraph", "codegraph.db-shm"), []byte("search index state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := project.New(store).Add(ctx, project.AddInput{Path: parent, ProjectID: ptr("ws-codegraph"), AsWorkspace: true}); err != nil {
		t.Fatalf("add workspace: %v", err)
	}
	rows, err := store.ListWorkspaceRepos(ctx, "ws-codegraph")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "api" || rows[0].GitStatus != domain.GitStatusReady || rows[1].Name != "notes" || rows[1].GitStatus != domain.GitStatusNeedsInit {
		t.Errorf("registered children = %#v, want api repository and notes asset only", rows)
	}
	tracked, err := exec.Command("git", "-C", parent, "ls-files", "--", ".codegraph").CombinedOutput()
	if err != nil || len(tracked) != 0 {
		t.Errorf("tracked CodeGraph files = %q, %v, want none", tracked, err)
	}
}
