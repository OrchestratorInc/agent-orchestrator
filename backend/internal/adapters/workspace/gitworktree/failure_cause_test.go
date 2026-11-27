package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// realExitError returns a genuine *exec.ExitError on every platform: the test
// binary rejects an unknown flag with a non-zero exit.
func realExitError(t *testing.T) error {
	t.Helper()
	err := exec.Command(os.Args[0], "-test.no-such-flag").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an exit error, got %v", err)
	}
	return err
}

func TestCommandErrorFailureCause(t *testing.T) {
	exitErr := realExitError(t)
	for _, tc := range []struct {
		output string
		err    error
		want   string
	}{
		{"fatal: a branch named 'ao/w-1' already exists", exitErr, "branch_exists"},
		{"fatal: 'ao/w-1' is already used by worktree at '/x'", exitErr, "branch_exists"},
		{"fatal: '/tmp/w-1' already exists", exitErr, "path_exists"},
		{"fatal: Unable to create '/r/.git/index.lock': File exists.", exitErr, "locked"},
		{"error: could not lock config file /r/.git/config: File exists", exitErr, "locked"},
		{"fatal: '/tmp/w-1' is a missing but locked worktree;", exitErr, "locked"},
		{"error: could not lock config file .git/config: Permission denied", exitErr, "permission"},
		{"error: unable to create file a.txt: Access is denied.", exitErr, "permission"},
		{"fatal: could not create work tree dir '/x': Read-only file system", exitErr, "permission"},
		{"fatal: cannot create directory at 'a/b': Filename too long", exitErr, "path_too_long"},
		{"fatal: not a git repository (or any of the parent directories): .git", exitErr, "not_a_repo"},
		{"fatal: detected dubious ownership in repository at '/r'", exitErr, "unsafe_repo"},
		{"fatal: cannot change to '/gone': No such file or directory", exitErr, "repo_missing"},
		{"error: unable to write file: No space left on device", exitErr, "disk_full"},
		{"error: unable to unlink old 'a.txt': Device or resource busy", exitErr, "in_use"},
		{"error: object file .git/objects/ab/cd is empty\nfatal: loose object abcd is corrupt", exitErr, "repo_corrupt"},
		{"fatal: index file corrupt", exitErr, "repo_corrupt"},
		{"fatal: a branch named 'fix-corrupt-cache' already exists", exitErr, "branch_exists"},
		{"fatal: cannot lock ref 'refs/heads/ao/w-1': 'refs/heads/ao' exists; cannot create 'refs/heads/ao/w-1'", exitErr, "ref_conflict"},
		{"fatal: invalid reference: origin/main", exitErr, "ref_missing"},
		{"fatal: Needed a single revision", exitErr, "ref_missing"},
		{"xcrun: error: invalid active developer path (/Library/Developer/CommandLineTools)", exitErr, "git_missing"},
		{"", &exec.Error{Name: "git", Err: exec.ErrNotFound}, "git_missing"},
		{"", &os.PathError{Op: "fork/exec", Path: "/opt/git", Err: os.ErrNotExist}, "git_missing"},
		{"", errors.New("signal: killed"), "killed"},
		{"fatal: unable to access 'https://x/': Could not resolve host", exitErr, "git_error"},
		{"fatal: unable to access 'https://x/': Could not resolve host", errors.New("exit status 128"), "other"},
	} {
		err := fmt.Errorf("spawn: %w", commandError{args: []string{"git"}, output: tc.output, err: tc.err})
		var classified interface{ FailureCause() string }
		if !errors.As(err, &classified) {
			t.Fatalf("%q: commandError does not expose FailureCause", tc.output)
		}
		if got := classified.FailureCause(); got != tc.want {
			t.Errorf("FailureCause(%q, %v) = %q, want %q", tc.output, tc.err, got, tc.want)
		}
		if !telemetrymeta.IsSpawnFailureCause(tc.want) {
			t.Errorf("cause %q is not in the closed vocabulary Sentry accepts", tc.want)
		}
	}
}

func TestKilledGitProcessIsKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX signal semantics")
	}
	cmd := exec.Command("sh", "-c", "kill -9 $$")
	err := cmd.Run()
	if got := (commandError{err: err}).FailureCause(); got != "killed" {
		t.Fatalf("FailureCause(%v) = %q, want killed", err, got)
	}
}

// TestNotARepoAtWrapsOnlyNotARepoFailures covers #8905: a git command refused
// because its folder is not a Git repository becomes a typed error naming the
// registered folder; any other git failure passes through unchanged.
func TestNotARepoAtWrapsOnlyNotARepoFailures(t *testing.T) {
	exitErr := errors.New("exit status 128")
	notRepo := notARepoAt("/work/ws", commandError{
		args:   []string{"git", "-C", "/work/ws", "worktree", "list", "--porcelain"},
		output: "fatal: not a git repository (or any of the parent directories): .git",
		err:    exitErr,
	})
	var typed *ports.WorkspaceNotARepoError
	if !errors.As(notRepo, &typed) || typed.Path != "/work/ws" {
		t.Fatalf("notARepoAt = %v, want *WorkspaceNotARepoError for /work/ws", notRepo)
	}
	if !errors.Is(notRepo, ports.ErrWorkspaceNotARepo) {
		t.Fatalf("errors.Is(%v, ErrWorkspaceNotARepo) = false, want true", notRepo)
	}
	var classified interface{ FailureCause() string }
	if !errors.As(notRepo, &classified) || classified.FailureCause() != "not_a_repo" {
		t.Fatalf("wrapped error lost the git failure cause for telemetry: %v", notRepo)
	}
	other := notARepoAt("/work/ws", commandError{
		args:   []string{"git", "-C", "/work/ws", "worktree", "add"},
		output: "fatal: '/tmp/w-1' already exists",
		err:    exitErr,
	})
	if errors.Is(other, ports.ErrWorkspaceNotARepo) {
		t.Fatalf("errors.Is(%v, ErrWorkspaceNotARepo) = true, want false", other)
	}
}

// TestWorkspaceIntegrationCreateReportsProjectNotARepo runs the real chain
// #8905 reports: a registered repository whose .git was deleted fails Create
// with a typed error naming that folder, while a session worktree that is not
// a repository is never reported as the project's folder.
func TestWorkspaceIntegrationCreateReportsProjectNotARepo(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"todo-app": repo}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}

	_, err = ws.Create(context.Background(), orchestratorTestConfig())
	var typed *ports.WorkspaceNotARepoError
	if !errors.As(err, &typed) {
		t.Fatalf("create error = %v, want *ports.WorkspaceNotARepoError", err)
	}
	wantPath, pathErr := physicalAbs(repo)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if typed.Path != wantPath {
		t.Fatalf("not-a-repo path = %q, want %q", typed.Path, wantPath)
	}

	// A git command run in a non-repository session folder keeps its raw
	// error: only registered repositories get project-level guidance.
	if _, err := ws.isDirty(context.Background(), filepath.Join(tmp, "managed")); err == nil || errors.Is(err, ports.ErrWorkspaceNotARepo) {
		t.Fatalf("isDirty on a non-repository worktree folder = %v, want a raw git error", err)
	}
}
