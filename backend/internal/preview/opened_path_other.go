//go:build !linux && !darwin && !windows

package preview

import "os"

// openedPath cannot read an open file's path on this platform: no artifact
// file's resolved path is named, and the desktop leaves such files out of a
// shared page.
func openedPath(*os.File) (string, bool) {
	return "", false
}
