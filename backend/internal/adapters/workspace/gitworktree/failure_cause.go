package gitworktree

import (
	"errors"
	"os/exec"
	"strings"
)

// FailureCause classifies a failed git command into the fixed vocabulary spawn
// telemetry reports (branch_exists, path_exists, locked, permission,
// path_too_long, not_a_repo, killed, other). It reads only git's stderr and
// exit state; nothing from the output leaves the daemon.
func (e commandError) FailureCause() string {
	return classifyGitFailure(e.output, e.err)
}

func classifyGitFailure(output string, err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && !exitErr.Exited() {
		return "killed"
	}
	if err != nil && strings.Contains(err.Error(), "signal: killed") {
		return "killed"
	}
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "not a git repository"):
		return "not_a_repo"
	case strings.Contains(text, "file name too long") || strings.Contains(text, "filename too long"):
		return "path_too_long"
	case strings.Contains(text, "permission denied") || strings.Contains(text, "operation not permitted"):
		return "permission"
	case strings.Contains(text, ".lock") || strings.Contains(text, "is locked") || strings.Contains(text, "locked working tree"):
		return "locked"
	case strings.Contains(text, "branch") && strings.Contains(text, "already exists"),
		strings.Contains(text, "is already checked out"), strings.Contains(text, "is already used by worktree"):
		return "branch_exists"
	case strings.Contains(text, "already exists"):
		return "path_exists"
	}
	return "other"
}
