package gitworktree

import (
	"context"
	"fmt"
	"strings"
)

// Pinned base commits.
//
// A caller that planned from an exact commit (a workflow attempt, for example)
// passes it as WorkspaceConfig.BaseSHA. Everything here exists so that pin is
// either honoured exactly or refused: the adapter never falls back to the
// repository default branch, because the fallback is precisely the defect this
// succeeds — a session branch seeded from "whatever the default is now" when the
// caller asked for an older commit.

// resolvePinnedBaseSHA validates a caller-supplied commit id against THIS
// repository and returns the full commit id git resolved it to.
//
// The validation is `git -C <repo> rev-parse --verify --end-of-options
// <sha>^{commit}`: run with -C <repo>, so a commit id belonging to a different
// repository cannot resolve, and with --end-of-options, so a value such as
// "--all" is a bad revision rather than a flag. The `^{commit}` peel accepts a
// tag object that points at a commit and rejects a blob or tree.
//
// Only a FULL 40-character hexadecimal id is accepted. `rev-parse` would also
// resolve "HEAD~1" or a 7-character abbreviation, and accepting either would
// reintroduce the defect being fixed under a different spelling: those inputs
// name a moving or ambiguous commit rather than the one the caller planned from.
//
// An empty value is not a pin and returns ("", nil): every caller keeps the
// default-branch behaviour it had before this field existed. A value that is
// NOT empty but only whitespace is garbage, not an unset field, and is refused:
// silently reading "   " as "no pin" would give a caller that meant to pin
// something the default branch instead.
func (w *Workspace) resolvePinnedBaseSHA(ctx context.Context, repo, requested string) (string, error) {
	if requested == "" {
		return "", nil
	}
	if strings.TrimSpace(requested) != requested {
		return "", fmt.Errorf("%w: %q is not a full 40-character hexadecimal commit id", ErrBaseSHAUnavailable, requested)
	}
	sha := requested
	if len(sha) != 40 || !isHexObjectName(sha) {
		return "", fmt.Errorf("%w: %q is not a full 40-character hexadecimal commit id", ErrBaseSHAUnavailable, requested)
	}
	out, err := w.run(ctx, w.binary, revParseCommitArgs(repo, sha)...)
	if err != nil {
		return "", fmt.Errorf("%w: commit %s does not resolve in %s: %w", ErrBaseSHAUnavailable, sha, repo, err)
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return "", fmt.Errorf("%w: commit %s produced no object id in %s", ErrBaseSHAUnavailable, sha, repo)
	}
	if !strings.EqualFold(resolved, sha) {
		// rev-parse answered a different object than the one requested. That
		// should be impossible for a full id, so it is reported rather than
		// silently used.
		return "", fmt.Errorf("%w: commit %s resolved to %s in %s", ErrBaseSHAUnavailable, sha, resolved, repo)
	}
	return resolved, nil
}

// isHexObjectName reports whether value is exactly a hexadecimal object name.
func isHexObjectName(value string) bool {
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return value != ""
}

// branchTipAt reports whether refs/heads/<branch> exists in repo and, when it
// does, whether its tip is exactly sha.
//
// This is the idempotent-retry rule for a pinned spawn: re-running the same
// spawn finds the branch the first attempt created, and that branch is accepted
// only while it still sits on the pinned commit. A branch that moved (an agent
// committed on top, or a different spawn created it from somewhere else) is not
// silently reused.
func (w *Workspace) branchTipAt(ctx context.Context, repo, branch, sha string) (exists bool, atSHA bool, err error) {
	exists, err = w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil || !exists {
		return exists, false, err
	}
	tip, err := w.revParse(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return true, false, err
	}
	return true, strings.EqualFold(tip, sha), nil
}

// worktreeHead reads the commit a worktree path currently has checked out.
func (w *Workspace) worktreeHead(ctx context.Context, path string) (string, error) {
	out, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("gitworktree: read HEAD of %q: %w", path, err)
	}
	head := strings.TrimSpace(string(out))
	if head == "" {
		return "", fmt.Errorf("gitworktree: %q reported no HEAD", path)
	}
	return head, nil
}
