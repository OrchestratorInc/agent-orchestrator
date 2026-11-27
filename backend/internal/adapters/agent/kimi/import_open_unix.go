//go:build !windows

package kimi

import (
	"os"
	"syscall"
)

// Nonblocking/no-follow opens keep a file replaced by a pipe or link between
// lstat and open from blocking an import or redirecting its reads.
func openKimiNativeFile(root *os.Root, name string, directory bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	return root.OpenFile(name, flags, 0)
}
