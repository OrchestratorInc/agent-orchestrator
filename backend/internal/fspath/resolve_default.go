//go:build !windows

package fspath

import "path/filepath"

// Resolved is the path with every symlink followed, as the filesystem reads
// it. The path must exist.
func Resolved(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
