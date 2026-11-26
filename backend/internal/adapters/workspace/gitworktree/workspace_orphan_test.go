package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// orchestratorTestConfig mirrors the config the session manager hands Create
// for a project's orchestrator: its worktree path is fixed per project, which
// is what lets one leftover folder block every later orchestrator (#7358).
func orchestratorTestConfig() ports.WorkspaceConfig {
	return ports.WorkspaceConfig{
		ProjectID:     "todo-app",
		SessionID:     "todo-app-orchestrator",
		Kind:          domain.KindOrchestrator,
		SessionPrefix: "todo-app",
		Branch:        "ao/todo-app-orchestrator",
		BaseBranch:    "main",
	}
}

func newOrphanTestWorkspace(t *testing.T) (ws *Workspace, git, repo, tmp string) {
	t.Helper()
	git = requireGit(t)
	tmp = t.TempDir()
	repo = setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"todo-app": repo}})
	if err != nil {
		t.Fatal(err)
	}
	return ws, git, repo, tmp
}

// orphanWorktree drops git's registration of the worktree at path the way an
// earlier cleanup can: the admin dir under <common-dir>/worktrees is gone, so
// `git worktree list` no longer shows path while the folder, and its `.git`
// file pointing at the vanished admin dir, stay on disk.
func orphanWorktree(t *testing.T, git, repo, path string) {
	t.Helper()
	gitFile, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		t.Fatalf("read worktree .git file: %v", err)
	}
	adminDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(gitFile)), "gitdir:"))
	if err := os.RemoveAll(adminDir); err != nil {
		t.Fatalf("remove worktree admin dir: %v", err)
	}
	runGit(t, git, repo, "worktree", "prune")
	if list := gitOutput(t, git, repo, "worktree", "list", "--porcelain"); strings.Contains(list, filepath.Base(path)) {
		t.Fatalf("worktree still registered after orphaning:\n%s", list)
	}
}

func orphanedSiblings(t *testing.T, path string) []string {
	t.Helper()
	matches, err := filepath.Glob(path + ".orphaned-*")
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestWorkspaceIntegrationCreateOrchestratorMovesOrphanedWorktreeAside(t *testing.T) {
	ws, git, repo, _ := newOrphanTestWorkspace(t)
	ctx := context.Background()
	cfg := orchestratorTestConfig()

	first, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "uncommitted.txt"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphanWorktree(t, git, repo, first.Path)

	second, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create over orphaned worktree folder: %v", err)
	}
	if second.Path != first.Path || second.Branch != cfg.Branch {
		t.Fatalf("second create = %#v, want path %q branch %q", second, first.Path, cfg.Branch)
	}
	if list := gitOutput(t, git, repo, "worktree", "list", "--porcelain"); !strings.Contains(list, "worktree "+second.Path+"\n") {
		t.Fatalf("new orchestrator worktree not registered:\n%s", list)
	}
	if _, err := os.Stat(filepath.Join(second.Path, "uncommitted.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new worktree inherited the orphan's files: stat err = %v", err)
	}
	aside := orphanedSiblings(t, second.Path)
	if len(aside) != 1 {
		t.Fatalf("orphaned siblings = %v, want exactly one", aside)
	}
	data, err := os.ReadFile(filepath.Join(aside[0], "uncommitted.txt"))
	if err != nil || string(data) != "keep me\n" {
		t.Fatalf("moved-aside folder lost its files: %q, %v", data, err)
	}
}

// TestWorkspaceIntegrationCreateMovesOrphanWithRelativeGitdirAside covers a
// worktree whose `.git` file holds a relative gitdir (what git writes with
// worktree.useRelativePaths): it must resolve against the worktree, as git does.
func TestWorkspaceIntegrationCreateMovesOrphanWithRelativeGitdirAside(t *testing.T) {
	ws, git, repo, _ := newOrphanTestWorkspace(t)
	ctx := context.Background()
	cfg := orchestratorTestConfig()

	first, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	gitFile := filepath.Join(first.Path, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		t.Fatal(err)
	}
	adminDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	rel, err := filepath.Rel(first.Path, adminDir)
	if err != nil {
		t.Fatal(err)
	}
	orphanWorktree(t, git, repo, first.Path)
	if err := os.WriteFile(gitFile, []byte("gitdir: "+filepath.ToSlash(rel)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ws.Create(ctx, cfg); err != nil {
		t.Fatalf("create over orphan with relative gitdir: %v", err)
	}
	if aside := orphanedSiblings(t, first.Path); len(aside) != 1 {
		t.Fatalf("orphaned siblings = %v, want exactly one", aside)
	}
}

func TestWorkspaceIntegrationCreateRefusesOccupiedPathAndLeavesItUntouched(t *testing.T) {
	cases := []struct {
		name  string
		fresh bool
		// occupy fills path with something that is not an orphaned worktree of
		// the project repo and returns a file inside it to check afterwards.
		occupy func(t *testing.T, git, tmp, path string) string
	}{
		{
			name: "unrelated folder",
			occupy: func(t *testing.T, _, _, path string) string {
				return writeFileIn(t, path, "notes.txt")
			},
		},
		{
			name:  "unrelated folder on fresh branch",
			fresh: true,
			occupy: func(t *testing.T, _, _, path string) string {
				return writeFileIn(t, path, "notes.txt")
			},
		},
		{
			name: "full clone",
			occupy: func(t *testing.T, git, _, path string) string {
				run(t, git, "init", path)
				return writeFileIn(t, path, "notes.txt")
			},
		},
		{
			name: "orphaned worktree of another repository",
			occupy: func(t *testing.T, git, tmp, path string) string {
				other := setupOriginClone(t, git, filepath.Join(tmp, "other"))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				runGit(t, git, other, "worktree", "add", "-b", "elsewhere", path, "main")
				orphanWorktree(t, git, other, path)
				return writeFileIn(t, path, "notes.txt")
			},
		},
		{
			name: "regular file",
			occupy: func(t *testing.T, _, _, path string) string {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, git, repo, tmp := newOrphanTestWorkspace(t)
			cfg := orchestratorTestConfig()
			cfg.FreshBranch = tc.fresh
			path, err := ws.managedPath(cfg)
			if err != nil {
				t.Fatal(err)
			}
			kept := tc.occupy(t, git, tmp, path)

			info, err := ws.Create(context.Background(), cfg)
			if !errors.Is(err, ports.ErrWorkspacePathOccupied) {
				t.Fatalf("create error = %v, want ports.ErrWorkspacePathOccupied", err)
			}
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				t.Fatalf("create error = %v, must not report a dirty rollback", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("create error = %v, want it to name %q", err, path)
			}
			if info != (ports.WorkspaceInfo{}) {
				t.Fatalf("create info = %#v, want zero", info)
			}
			if data, err := os.ReadFile(kept); err != nil || string(data) != "keep me\n" {
				t.Fatalf("occupying content changed: %q, %v", data, err)
			}
			if aside := orphanedSiblings(t, path); len(aside) != 0 {
				t.Fatalf("occupied path was moved aside to %v", aside)
			}
			if list := gitOutput(t, git, repo, "worktree", "list", "--porcelain"); strings.Contains(list, path) {
				t.Fatalf("occupied path got registered:\n%s", list)
			}
		})
	}
}

func TestWorkspaceIntegrationCreateAddsIntoEmptyDirectoryInPlace(t *testing.T) {
	ws, _, _, _ := newOrphanTestWorkspace(t)
	cfg := orchestratorTestConfig()
	path, err := ws.managedPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := ws.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create into empty directory: %v", err)
	}
	if info.Path != path {
		t.Fatalf("create path = %q, want %q", info.Path, path)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Fatalf("worktree not checked out into empty directory: %v", err)
	}
	if aside := orphanedSiblings(t, path); len(aside) != 0 {
		t.Fatalf("empty directory was moved aside to %v", aside)
	}
}

func TestMovePathAsideSkipsTakenDestinations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wt")
	writeFileIn(t, path, "a.txt")
	writeFileIn(t, path+".orphaned-x", "b.txt")

	got, err := movePathAside(path, ".orphaned-x")
	if err != nil {
		t.Fatal(err)
	}
	if want := path + ".orphaned-x-2"; got != want {
		t.Fatalf("movePathAside = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(got, "a.txt")); err != nil {
		t.Fatalf("moved folder lost its file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path+".orphaned-x", "b.txt")); err != nil {
		t.Fatalf("existing destination was disturbed: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still present: %v", err)
	}
}

func writeFileIn(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}
