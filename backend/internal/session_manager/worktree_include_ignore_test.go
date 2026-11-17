package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	workspacerouter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/router"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func newIncludedWorktree(t *testing.T, project string) (string, *gitworktree.Workspace) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	runManagerGit(t, project, "worktree", "add", "--detach", workspace, "HEAD")
	adapter, err := gitworktree.New(gitworktree.Options{
		ManagedRoot: root, RepoResolver: gitworktree.StaticRepoResolver{"project": project},
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, adapter
}

func TestWorktreeIncludeRejectsSourceOnlyIgnore(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "private configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	if err := (&Manager{workspace: adapter}).provisionWorkspace(context.Background(), domain.ProjectRecord{Path: project}, workspace); err != nil {
		t.Fatalf("skip a source-only ignored file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".env")); !os.IsNotExist(err) {
		t.Fatalf("private file became visible to Git: %v", err)
	}
}

func TestWorktreeIncludeTemporaryCopiesStayIgnored(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore private configuration")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "private configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	if err := (&Manager{workspace: adapter}).provisionWorkspace(context.Background(), domain.ProjectRecord{Path: project}, workspace); err != nil {
		t.Fatal(err)
	}
	// A stopped process cannot run deferred cleanup. Model the file it leaves.
	writeTestFile(t, filepath.Join(workspace, ".ao-include-orphan"), "partial private configuration")
	visible := runManagerGit(t, workspace, "ls-files", "--others", "--exclude-standard")
	if strings.Contains(visible, ".ao-include-orphan") {
		t.Fatal("an abandoned temporary copy is eligible for Git staging")
	}
}

func TestWorktreeIncludeRejectsUnignoredTemporaryCopy(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore private configuration")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "private configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	writeTestFile(t, filepath.Join(workspace, ".gitignore"), ".env\n!.ao-include-*\n")
	if err := (&Manager{workspace: adapter}).provisionWorkspace(context.Background(), domain.ProjectRecord{Path: project}, workspace); err == nil {
		t.Error("copy succeeded when its temporary file would be visible to Git")
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".env")); !os.IsNotExist(err) {
		t.Fatalf("copy started before temporary-file protection: %v", err)
	}
}

func TestWorktreeIncludeExclusionFailureStopsCopy(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore private configuration")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "private configuration")
	workspace, _ := newIncludedWorktree(t, project)
	failure := errors.New("cannot protect temporary copies")
	m := &Manager{workspace: &fakeWorkspace{addExcludeErr: failure}}
	if err := m.provisionWorkspace(context.Background(), domain.ProjectRecord{Path: project}, workspace); !errors.Is(err, failure) {
		t.Fatalf("copy error = %v, want exclusion error", err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".env")); !os.IsNotExist(err) {
		t.Fatalf("copy started before temporary-file protection: %v", err)
	}
}

func TestWorktreeIncludeRoutesExclusionToProject(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore private configuration")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "private configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	m := &Manager{workspace: workspacerouter.New(workspacerouter.Deps{Git: adapter, Scratch: &fakeWorkspace{}})}
	if err := m.provisionWorkspace(context.Background(), domain.ProjectRecord{ID: "project", Path: project}, workspace); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, ".env")); err != nil || string(got) != "private configuration" {
		t.Fatalf("project file was not copied: %q, %v", got, err)
	}
}
