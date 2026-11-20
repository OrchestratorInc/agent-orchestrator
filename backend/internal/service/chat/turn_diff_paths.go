package chat

import (
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/fspath"
)

// pathOutsideSession reports whether a turn-diff path names a file in neither
// the session worktree nor its artifact directory, such as the memory file an
// agent keeps under its own home directory. The Files panel serves only the
// worktree, so a client must not offer such a row as a file it can open.
//
// A relative path is the provider naming a worktree file, and an unknown
// worktree claims nothing, so both report false.
func pathOutsideSession(workspaceDir, artifactDir, path string) bool {
	if workspaceDir == "" || !filepath.IsAbs(path) {
		return false
	}
	for _, root := range []string{workspaceDir, artifactDir} {
		if root != "" && pathUnderRoot(root, path) {
			return false
		}
	}
	return true
}

// pathUnderRoot compares both paths as the filesystem resolves them, with the
// session's workspace resolver: a worktree reached by another spelling (macOS
// /var and /private/var) still owns its files, a link inside the worktree
// that points out of it does not, a ".." applies after the link before it,
// and a deleted file keeps the directories that no longer exist.
func pathUnderRoot(root, path string) bool {
	return within(fspath.ResolveInOrder(root), fspath.ResolveInOrder(path))
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
