package session

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxConflictFiles caps the files named for one PR; the view lists a few and
// the banner says where to look, so a huge conflict does not flood the summary.
const maxConflictFiles = 50

// conflictCache remembers conflict files by the two commits they were computed
// from. Both are immutable SHAs, so an entry never goes stale; a new push or a
// moved base is a new key.
type conflictCache struct {
	mu    sync.Mutex
	files map[string][]string
}

func (c *conflictCache) get(key string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	files, ok := c.files[key]
	return files, ok
}

func (c *conflictCache) put(key string, files []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = map[string][]string{}
	}
	// A bounded memory is enough: the keys of PRs that moved on are never asked for again.
	if len(c.files) >= 256 {
		c.files = map[string][]string{}
	}
	c.files[key] = files
}

// prConflictFiles names the files a PR's head conflicts with its base in, by
// merging the two in memory in the session's own repository. It reads local
// objects only: no provider call, and neither HEAD, the index nor the worktree
// is touched. It reports nothing when either side is not available locally,
// since an unknown answer must not read as "no conflicts".
func (s *Service) prConflictFiles(ctx context.Context, root string, pr domain.PullRequest) []string {
	if root == "" || pr.HeadSHA == "" || !gitCommitExists(ctx, root, pr.HeadSHA) {
		return nil
	}
	baseSHA := ""
	for _, candidate := range workspaceBaseRefCandidates(pr.TargetBranch) {
		if out, err := gitWorkspaceOutput(ctx, root, "rev-parse", "--verify", "--quiet", candidate+"^{commit}"); err == nil {
			baseSHA = strings.TrimSpace(out)
			break
		}
	}
	if baseSHA == "" {
		return nil
	}
	key := root + "\x00" + baseSHA + "\x00" + pr.HeadSHA
	if files, ok := s.conflicts.get(key); ok {
		return files
	}
	files, ok := gitMergeConflictFiles(ctx, root, baseSHA, pr.HeadSHA)
	if !ok {
		return nil
	}
	s.conflicts.put(key, files)
	return files
}

// gitMergeConflictFiles runs `git merge-tree --write-tree` for two commits. It
// exits 0 for a clean merge and 1 when files conflict, listing them after the
// result tree; anything else (an old git without --write-tree, a bad object) is
// "unknown", not "no conflicts".
func gitMergeConflictFiles(ctx context.Context, root, base, head string) ([]string, bool) {
	cmd := workspaceGitCommand(ctx, root, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", base, head)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return []string{}, true
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		// <tree>\0<path>\0<path>\0 ...
		parts := strings.Split(stdout.String(), "\x00")
		seen := map[string]bool{}
		files := []string{}
		for _, part := range parts[1:] {
			if part != "" && !seen[part] {
				seen[part] = true
				files = append(files, part)
			}
		}
		sort.Strings(files)
		if len(files) > maxConflictFiles {
			files = files[:maxConflictFiles]
		}
		return files, true
	default:
		return nil, false
	}
}
