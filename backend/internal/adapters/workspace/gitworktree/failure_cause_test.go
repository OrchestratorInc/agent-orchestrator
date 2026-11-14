package gitworktree

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
)

func TestCommandErrorFailureCause(t *testing.T) {
	exitErr := errors.New("exit status 128")
	for _, tc := range []struct {
		output string
		err    error
		want   string
	}{
		{"fatal: a branch named 'ao/w-1' already exists", exitErr, "branch_exists"},
		{"fatal: 'ao/w-1' is already used by worktree at '/x'", exitErr, "branch_exists"},
		{"fatal: '/tmp/w-1' already exists", exitErr, "path_exists"},
		{"fatal: Unable to create '/r/.git/index.lock': File exists.", exitErr, "locked"},
		{"error: could not lock config file .git/config: Permission denied", exitErr, "permission"},
		{"fatal: cannot create directory at 'a/b': Filename too long", exitErr, "path_too_long"},
		{"fatal: not a git repository (or any of the parent directories): .git", exitErr, "not_a_repo"},
		{"", errors.New("signal: killed"), "killed"},
		{"fatal: unable to access 'https://x/': Could not resolve host", exitErr, "other"},
	} {
		err := fmt.Errorf("spawn: %w", commandError{args: []string{"git"}, output: tc.output, err: tc.err})
		var classified interface{ FailureCause() string }
		if !errors.As(err, &classified) {
			t.Fatalf("%q: commandError does not expose FailureCause", tc.output)
		}
		if got := classified.FailureCause(); got != tc.want {
			t.Errorf("FailureCause(%q, %v) = %q, want %q", tc.output, tc.err, got, tc.want)
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
