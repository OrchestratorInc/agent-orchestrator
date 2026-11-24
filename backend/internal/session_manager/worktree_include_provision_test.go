package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestProvisionWorkspaceWorktreeIncludeBeforeSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Setup command uses a POSIX shell")
	}
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "local configuration\n")
	runManagerGit(t, project, "add", ".gitignore", ".worktreeinclude")
	runManagerGit(t, project, "commit", "-m", "include local configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	record := domain.ProjectRecord{Path: project, Config: domain.ProjectConfig{
		PostCreate: []string{"test -f .env && test ! -L .env && cp .env setup-read"},
	}}
	if err := (&Manager{workspace: adapter}).provisionWorkspace(context.Background(), record, "", workspace); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "setup-read")); err != nil || string(got) != "local configuration\n" {
		t.Fatalf("Setup read = %q, %v", got, err)
	}
	writeTestFile(t, filepath.Join(workspace, ".env"), "session edit\n")
	if got, err := os.ReadFile(filepath.Join(project, ".env")); err != nil || string(got) != "local configuration\n" {
		t.Fatalf("source changed after session edit: %q, %v", got, err)
	}
}

func TestProvisionWorkspaceDirectorySymlinkWinsOverWorktreeInclude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires a host privilege outside this unit test")
	}
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), "config/\n")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), "config/\n")
	writeTestFile(t, filepath.Join(project, "config", "secrets.json"), "shared configuration")
	workspace := t.TempDir()
	record := domain.ProjectRecord{Path: project, Config: domain.ProjectConfig{Symlinks: []string{"config"}}}
	if err := (&Manager{}).provisionWorkspace(context.Background(), record, "", workspace); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(workspace, "config"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configured directory link was replaced: %v, %v", info, err)
	}
}
