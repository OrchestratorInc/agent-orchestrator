//go:build windows

package usage

import "io/fs"

// allocatedBytes is the file's length: FileInfo carries neither allocated
// size nor a link count on Windows, so hard links are counted each time.
func allocatedBytes(info fs.FileInfo, _ map[[2]any]struct{}) int64 {
	return info.Size()
}
