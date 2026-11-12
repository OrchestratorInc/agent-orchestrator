package chat

import (
	"path/filepath"
	"strings"
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

// pathUnderRoot compares both paths as the filesystem resolves them, as the
// session's workspace resolver does: a worktree reached by another spelling
// (macOS /var and /private/var) still owns its files, and a link inside the
// worktree that points out of it does not.
func pathUnderRoot(root, path string) bool {
	return within(resolveExistingPrefix(root), resolveExistingPrefix(path))
}

// resolveExistingPrefix resolves symlinks through the deepest ancestor that
// still exists and keeps the rest as written, so a deleted file whose
// directories went with it still resolves to the root that held it.
func resolveExistingPrefix(path string) string {
	path = filepath.Clean(path)
	for dir := path; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			rest, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return path
			}
			return filepath.Join(resolved, rest)
		}
		if filepath.Dir(dir) == dir {
			return path
		}
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
