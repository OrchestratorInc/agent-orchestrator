package gitworktree

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// FailureCause classifies a failed git command into the fixed vocabulary spawn
// telemetry reports (see classifyGitFailure). It reads only git's stderr and
// exit state; nothing from the output leaves the daemon.
func (e commandError) FailureCause() string {
	return classifyGitFailure(e.output, e.err)
}

// notARepoAt reports err as a *ports.WorkspaceNotARepoError naming repo when
// git refused to run in repo because it is not a Git repository. Callers pass
// only a registered project repository folder (never a session worktree), so
// the service layer can return project-level recovery guidance instead of
// git's raw exit 128. Any other failure is returned unchanged.
func notARepoAt(repo string, err error) error {
	var cmd commandError
	if errors.As(err, &cmd) && cmd.FailureCause() == "not_a_repo" {
		return &ports.WorkspaceNotARepoError{Path: repo, Err: err}
	}
	return err
}

// classifyGitFailure names why a git command failed, from a closed list:
// killed, git_missing, not_a_repo, unsafe_repo, repo_missing, path_too_long,
// disk_full, permission, in_use, repo_corrupt, ref_conflict, locked,
// branch_exists, path_exists, ref_missing, git_error (git ran and failed with
// a message not recognised here) or other (git did not run). Only messages
// from `git worktree` and `git status` are forced to English, so matching
// leans on fragments that also survive in commands run in the user's locale
// (quoted paths, errno text from the OS).
func classifyGitFailure(output string, err error) string {
	var exitErr *exec.ExitError
	ran := errors.As(err, &exitErr)
	if ran && !exitErr.Exited() {
		return "killed"
	}
	if err != nil && strings.Contains(err.Error(), "signal: killed") {
		return "killed"
	}
	// The binary could not be started at all: not on PATH, or a configured
	// path that no longer exists.
	if !ran && (errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)) {
		return "git_missing"
	}
	text := strings.ToLower(output)
	has := func(fragments ...string) bool {
		for _, fragment := range fragments {
			if strings.Contains(text, fragment) {
				return true
			}
		}
		return false
	}
	switch {
	// macOS ships a /usr/bin/git stub that fails until the Command Line
	// Tools are installed.
	case has("xcrun: error", "no developer tools were found", "requires the command line developer tools"):
		return "git_missing"
	case has("not a git repository"):
		return "not_a_repo"
	case has("dubious ownership", "unsafe repository", "safe.directory"):
		return "unsafe_repo"
	case has("file name too long", "filename too long"):
		return "path_too_long"
	case has("no space left on device", "disk quota exceeded", "not enough space on the disk"):
		return "disk_full"
	case has("permission denied", "operation not permitted", "access is denied", "read-only file system"):
		return "permission"
	case has("device or resource busy", "being used by another process"):
		return "in_use"
	// After the permission and disk checks: git cannot enter a folder that
	// exists but is unreadable either, and that is not a missing repository.
	case has("cannot change to"):
		return "repo_missing"
	// git's own phrasings, with spaces no branch name can contain, so a
	// quoted branch such as 'fix-corrupt-cache' is not read as corruption.
	case has(" is corrupt", "index file corrupt", "bad object ", "bad signature"):
		return "repo_corrupt"
	// A ref whose name is a directory of another ref (refs/heads/a vs
	// refs/heads/a/b) can never be created.
	case has("exists; cannot create"):
		return "ref_conflict"
	case has(".lock", "is locked", "locked working tree", "locked worktree", "could not lock"):
		return "locked"
	case has("is already checked out", "is already used by worktree"),
		strings.Contains(text, "branch") && strings.Contains(text, "already exists"):
		return "branch_exists"
	case has("already exists"):
		return "path_exists"
	case has("invalid reference", "not a valid object name", "unknown revision", "is not a commit", "needed a single revision", "couldn't find remote ref"):
		return "ref_missing"
	}
	if ran {
		return "git_error"
	}
	return "other"
}
