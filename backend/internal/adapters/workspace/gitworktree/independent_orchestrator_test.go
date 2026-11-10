package gitworktree

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 真实 Git 检查主编排器和两个独立编排器的目录并存，恢复不会切入另一个会话。
func TestIndependentOrchestratorWorktreesRemainSeparate(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	paths := map[string]bool{}
	for _, id := range []domain.SessionID{"primary", "independent-a", "independent-b", "workspace-a", "workspace-b"} {
		cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: id, Kind: domain.KindOrchestrator,
			IndependentOrchestrator: id != "primary", Branch: "ao/" + string(id)}
		var info ports.WorkspaceInfo
		if id == "workspace-a" || id == "workspace-b" {
			multi, createErr := ws.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
				ProjectID: cfg.ProjectID, SessionID: cfg.SessionID, Kind: cfg.Kind,
				IndependentOrchestrator: true, Branch: cfg.Branch, RootRepoPath: repo,
			})
			info, err = multi.Root, createErr
		} else {
			info, err = ws.Create(ctx, cfg)
		}
		if err != nil {
			t.Fatal(err)
		}
		if paths[info.Path] {
			t.Fatalf("workspace reused: %s", info.Path)
		}
		paths[info.Path] = true
		restored, err := ws.Restore(ctx, cfg)
		if err != nil || restored.Path != info.Path || restored.Branch != info.Branch {
			t.Fatalf("restore=%+v, err=%v", restored, err)
		}
	}
}
