// Package fspath resolves paths the way the filesystem reads them.
package fspath

import (
	"path/filepath"
	"strings"
)

// ResolveInOrder resolves an absolute path one component at a time,
// following each symlink before applying the ".." after it. Cleaning first
// would be wrong: with link -> /elsewhere/dir, "root/link/../file" names
// /elsewhere/file, not root/file. Once a component does not exist the rest is
// joined as written, since a missing directory holds no links to follow.
func ResolveInOrder(absolute string) string {
	volume := filepath.VolumeName(absolute)
	resolved := volume + string(filepath.Separator)
	parts := strings.FieldsFunc(absolute[len(volume):], func(r rune) bool { return r == '/' || r == filepath.Separator })
	for i, part := range parts {
		switch part {
		case ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next, err := Resolved(filepath.Join(resolved, part))
		if err != nil {
			return filepath.Join(append([]string{resolved}, parts[i:]...)...)
		}
		resolved = next
	}
	return resolved
}
