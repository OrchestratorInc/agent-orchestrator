//go:build !windows

package usage

import (
	"io/fs"
	"syscall"
)

// allocatedBytes is the disk space info's file occupies: its blocks, not its
// length. A file with several hard links is counted once per walk. The keys
// are any because the device and inode types differ by platform.
func allocatedBytes(info fs.FileInfo, links map[[2]any]struct{}) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}
	if stat.Nlink > 1 && !info.IsDir() {
		key := [2]any{stat.Dev, stat.Ino}
		if _, counted := links[key]; counted {
			return 0
		}
		links[key] = struct{}{}
	}
	return stat.Blocks * 512
}
