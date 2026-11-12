//go:build !windows

package project

import "path/filepath"

// resolveDirectoryIdentity resolves a directory's platform identity. On POSIX
// systems resolving symlinks is the complete answer, so EvalSymlinks suffices.
func resolveDirectoryIdentity(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
