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

// pathUnderRoot compares lexically first, then through symlinks, so a worktree
// reached by another spelling (macOS /var and /private/var) still owns its
// files. A path that no longer exists is resolved through its directory.
func pathUnderRoot(root, path string) bool {
	if within(filepath.Clean(root), filepath.Clean(path)) {
		return true
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		dir, dirErr := filepath.EvalSymlinks(filepath.Dir(path))
		if dirErr != nil {
			return false
		}
		resolved = filepath.Join(dir, filepath.Base(path))
	}
	return within(resolvedRoot, resolved)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
