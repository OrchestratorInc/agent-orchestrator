package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestPreRemoveValidatesAgainstRecordedWorktreeRoot covers the snapshot rule for
// teardown scripts: a session created under a project root that the project has
// since changed still validated against the RECORDED root, because that is where
// its worktree actually is.
func TestPreRemoveValidatesAgainstRecordedWorktreeRoot(t *testing.T) {
	m, st, _, _ := newCleanupFixture(t, "exit 0")
	customRoot := filepath.Join(t.TempDir(), "custom root")
	workspace := filepath.Join(customRoot, "mer", "mer-1")
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatal(err)
	}
	rec := mkLive("mer-2")
	rec.Metadata.WorkspacePath = workspace
	rec.Metadata.WorktreeRoot = customRoot
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	project.Path = t.TempDir()
	project.Config.PreRemove = []string{"exit 0"}
	st.projects["mer"] = project

	if err := m.runPreRemove(rec.ProjectID, workspace, rec.Metadata.WorktreeRoot); err != nil {
		t.Fatalf("runPreRemove with the recorded root: %v", err)
	}
	// Without the snapshot the path is judged against the installation default,
	// where this workspace is not: the steps must not run against it.
	if err := m.runPreRemove(rec.ProjectID, workspace, ""); err == nil {
		t.Fatal("runPreRemove without the snapshot = nil, want a refusal")
	}
}

// TestRestoreUsesRecordedWorktreeRoot covers the restore path: the workspace
// configuration handed to the adapter carries the recorded root, so a project
// whose configured root changed does not move an existing checkout.
func TestRestoreUsesRecordedWorktreeRoot(t *testing.T) {
	m, st, _, ws := newManager()
	customRoot := t.TempDir()
	rec := mkLive("mer-1")
	rec.Metadata.WorkspacePath = filepath.Join(customRoot, "mer", "mer-1")
	rec.Metadata.WorktreeRoot = customRoot
	rec.Metadata.Branch = "ao/mer-1"
	st.sessions[rec.ID] = rec
	project := st.projects["mer"]
	// The project now points somewhere else entirely.
	project.Config.WorktreeRoot = filepath.Join(t.TempDir(), "other root")
	st.projects["mer"] = project

	if _, err := m.restoreSessionWorkspace(context.Background(), project, rec); err != nil {
		t.Fatalf("restoreSessionWorkspace: %v", err)
	}
	if len(ws.restoreConfigs) == 0 {
		t.Fatal("no restore configuration was handed to the workspace adapter")
	}
	cfg := ws.restoreConfigs[len(ws.restoreConfigs)-1]
	if cfg.WorktreeRoot != customRoot {
		t.Fatalf("restore root = %q, want the recorded %q", cfg.WorktreeRoot, customRoot)
	}
	if cfg.Path != rec.Metadata.WorkspacePath {
		t.Fatalf("restore path = %q, want %q", cfg.Path, rec.Metadata.WorkspacePath)
	}
}

// TestSpawnRejectsMissingConfiguredWorktreeRoot covers the explicit failure: a
// project root that disappeared must fail the spawn with a named error instead
// of quietly materialising the session under the installation default.
func TestSpawnRejectsMissingConfiguredWorktreeRoot(t *testing.T) {
	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	missing := filepath.Join(t.TempDir(), "removed-root")
	project := st.projects["mer"]
	project.Config.WorktreeRoot = missing
	st.projects["mer"] = project

	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err == nil {
		t.Fatal("Spawn with a missing worktree root = nil error, want a refusal")
	}
	if !errors.Is(err, ErrWorktreeRootUnavailable) {
		t.Fatalf("Spawn error = %v, want ErrWorktreeRootUnavailable", err)
	}
}

// TestWorkspaceProjectCreatesUnderCustomRoot covers the workspace-project half of
// the root contract: CreateWorkspaceProject places every repo of the session
// under the project's root and reports that root on both the session root info
// and every repo row, which is what Spawn persists into session metadata.
func TestWorkspaceProjectCreatesUnderCustomRoot(t *testing.T) {
	m, st, _, ws := newManager()
	customRoot := filepath.Join(t.TempDir(), "workspace root")
	if err := os.MkdirAll(customRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	// Look the project up as a workspace project with a configured root; the fake
	// adapter records the configuration it was handed.
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	project.Config.WorktreeRoot = customRoot
	st.projects["mer"] = project

	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := ws.lastProjectCfg.WorktreeRoot; got != customRoot {
		t.Fatalf("workspace-project create root = %q, want %q", got, customRoot)
	}
}

// TestWorkspaceRestoreRootRowUsesRecordedSnapshot covers restore from markers: the
// session metadata may carry no root (rows written before the column existed),
// so the recorded root row is what supplies the snapshot.
func TestWorkspaceRestoreRootRowUsesRecordedSnapshot(t *testing.T) {
	f := newMultiRepoFixture(t)
	ctx := context.Background()
	const customRoot = "/recorded/workspace root"

	project := f.projectA
	project.Kind = domain.ProjectKindWorkspace
	if err := f.store.UpsertProject(ctx, project); err != nil {
		t.Fatalf("save workspace project: %v", err)
	}
	f.manager.store = f.store

	rec := domain.SessionRecord{
		ID:        "ws-restore-root",
		ProjectID: domain.ProjectID(project.ID),
		Kind:      domain.KindOrchestrator,
		// No WorktreeRoot: this is the pre-column shape the fix targets.
		Metadata: domain.SessionMetadata{
			Branch:        "ao/work",
			WorkspacePath: customRoot + "/" + string(project.ID) + "/ws-restore-root",
		},
	}
	row := domain.SessionWorktreeRecord{
		SessionID:    rec.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		ProjectID:    rec.ProjectID,
		RepoPath:     project.Path,
		Branch:       rec.Metadata.Branch,
		WorktreePath: rec.Metadata.WorkspacePath,
		WorktreeRoot: customRoot,
		State:        "active",
	}
	infos, err := f.manager.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, []domain.SessionWorktreeRecord{row})
	if err != nil {
		t.Fatalf("restore rows: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("no repo infos produced")
	}
	if infos[0].RepoName != domain.RootWorkspaceRepoName {
		t.Fatalf("first row = %q, want the root row", infos[0].RepoName)
	}
	if infos[0].WorktreeRoot != customRoot {
		t.Fatalf("root row snapshot = %q, want the recorded %q", infos[0].WorktreeRoot, customRoot)
	}
}
