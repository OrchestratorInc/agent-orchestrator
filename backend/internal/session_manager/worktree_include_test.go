package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCopyWorktreeInclude(t *testing.T) {
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n*.local\nsecrets/\nnot-included\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n*.local\nsecrets/*\n!secrets/skip.txt\nREADME.md\nuntracked.txt\n")

	writeTestFile(t, filepath.Join(project, ".env"), "X=1")
	writeTestFile(t, filepath.Join(project, "a.local"), "project")
	writeTestFile(t, filepath.Join(project, "secrets", "key.txt"), "key")
	if err := os.Chmod(filepath.Join(project, "secrets", "key.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(project, "secrets", "skip.txt"), "skip")
	writeTestFile(t, filepath.Join(project, "not-included"), "n")
	writeTestFile(t, filepath.Join(project, "untracked.txt"), "u")
	writeTestFile(t, filepath.Join(project, "README.md"), "project edit\n")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(project, ".env"), filepath.Join(project, "link.local")); err != nil {
			t.Fatal(err)
		}
	}

	workspace, adapter := newIncludedWorktree(t, project)
	writeTestFile(t, filepath.Join(workspace, "a.local"), "keep")

	if err := copyWorktreeInclude(context.Background(), domain.ProjectRecord{Path: project}, workspace, adapter); err != nil {
		t.Fatalf("copyWorktreeInclude: %v", err)
	}

	want := map[string]string{
		".env":                              "X=1",
		"a.local":                           "keep",
		filepath.Join("secrets", "key.txt"): "key",
		"README.md":                         "hello\n",
	}
	for rel, content := range want {
		data, err := os.ReadFile(filepath.Join(workspace, rel))
		if err != nil || string(data) != content {
			t.Errorf("%s = %q err=%v, want %q", rel, data, err, content)
		}
	}
	for _, rel := range []string{filepath.Join("secrets", "skip.txt"), "not-included", "untracked.txt", "link.local"} {
		if _, err := os.Lstat(filepath.Join(workspace, rel)); !os.IsNotExist(err) {
			t.Errorf("%s should not have been copied (err=%v)", rel, err)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(workspace, "secrets", "key.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("secrets/key.txt mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

// newIncludeWorkspace checks out project's HEAD as a linked worktree, the
// shape every session workspace has.
func newIncludeWorkspace(t *testing.T, project string) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	runManagerGit(t, project, "worktree", "add", "--detach", workspace, "HEAD")
	return workspace
}

func TestCopyWorktreeIncludeSkipsFilesTheWorkspaceDoesNotIgnore(t *testing.T) {
	project := newManagerGitRepo(t)
	workspace := newIncludeWorkspace(t, project)
	// Only the source checkout ignores the file: the rule is not committed.
	writeTestFile(t, filepath.Join(project, ".gitignore"), "source-only.settings\n")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), "source-only.settings\n")
	writeTestFile(t, filepath.Join(project, "source-only.settings"), "X=1")
	if out := runManagerGit(t, project, "check-ignore", "source-only.settings"); strings.TrimSpace(out) != "source-only.settings" {
		t.Fatalf("source does not ignore the file: %q", out)
	}

	if err := copyWorktreeInclude(context.Background(), domain.ProjectRecord{Path: project}, workspace, nil); err != nil {
		t.Fatalf("copyWorktreeInclude: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, "source-only.settings")); !os.IsNotExist(err) {
		t.Fatalf("file the workspace does not ignore was copied (err=%v)", err)
	}
}

func TestCopyWorktreeIncludeWithoutIncludeFileIsNoop(t *testing.T) {
	// Not a Git repository: without a .worktreeinclude, Git is never run.
	if err := copyWorktreeInclude(context.Background(), domain.ProjectRecord{Path: t.TempDir()}, t.TempDir(), nil); err != nil {
		t.Fatalf("copyWorktreeInclude: %v", err)
	}
}

func TestProvisionWorkspaceSymlinkWinsOverWorktreeInclude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires a host privilege outside this unit test")
	}
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), ".env\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), ".env\n")
	writeTestFile(t, filepath.Join(project, ".env"), "X=1")
	workspace := t.TempDir()

	record := domain.ProjectRecord{Path: project, Config: domain.ProjectConfig{Symlinks: []string{".env"}}}
	if err := (&Manager{}).provisionWorkspace(context.Background(), record, workspace); err != nil {
		t.Fatalf("provisionWorkspace: %v", err)
	}
	info, err := os.Lstat(filepath.Join(workspace, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".env mode = %v, want the configured symlink", info.Mode())
	}
}
