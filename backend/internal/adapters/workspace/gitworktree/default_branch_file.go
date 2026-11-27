package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
)

// ReadDefaultBranchFile reads a regular file from the registered repository's
// default remote-tracking commit. A PR's base can itself be a worker branch,
// so the PR's base, head and worktree do not participate in this lookup.
func (w *Workspace) ReadDefaultBranchFile(ctx context.Context, repo, branch, path string) (string, error) {
	branch = strings.TrimSpace(branch)
	path = strings.TrimSpace(path)
	rel := filepath.ToSlash(filepath.Clean(path))
	if !filepath.IsLocal(path) || strings.HasPrefix(path, "\\") || rel == "." || !fs.ValidPath(rel) {
		return "", errors.New("path must identify a file inside the repository")
	}
	if strings.TrimSpace(repo) == "" {
		return "", errors.New("registered repository path is required")
	}
	var ref string
	if branch == "" {
		target, err := gitdefault.New(w.binary, gitdefault.Runner(w.run)).Inspect(ctx, repo)
		if err != nil {
			return "", err
		}
		ref = target.Ref
	} else {
		target, err := w.ResolveDefaultBranch(ctx, repo, branch)
		if err != nil {
			return "", err
		}
		ref = target.BaseRef
	}
	if !strings.HasPrefix(ref, "refs/remotes/") {
		return "", errors.New("default branch needs a remote-tracking ref")
	}

	// Pin the ref before walking its tree. Object replacement and recursive
	// path traversal through symlinks cannot select a different file.
	git := func(args ...string) ([]byte, error) {
		return w.run(ctx, w.binary, append([]string{"-C", repo, "--no-replace-objects", "--literal-pathspecs"}, args...)...)
	}
	commit, err := git("rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve default branch commit: %w", err)
	}
	entry, err := git("ls-tree", "-z", "--full-tree", strings.TrimSpace(string(commit)), "--", rel)
	if err != nil {
		return "", fmt.Errorf("find file in default branch: %w", err)
	}
	meta, name, _ := strings.Cut(strings.TrimSuffix(string(entry), "\x00"), "\t")
	fields := strings.Fields(meta)
	if name != rel || len(fields) != 3 || (fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" {
		return "", errors.New("path must identify a regular file in the default branch")
	}
	body, err := git("cat-file", "blob", fields[2])
	if err != nil {
		return "", fmt.Errorf("read default branch file: %w", err)
	}
	return string(body), nil
}
