//go:build darwin

package main

import (
	"io/fs"
	"strconv"
	"syscall"
)

// fileChangeID identifies a file's inode and status change time, which a
// rewrite changes even when it restores the size and modification time.
func fileChangeID(info fs.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(stat.Ino, 10) + ":" + strconv.FormatInt(stat.Ctimespec.Sec, 10) + "." + strconv.FormatInt(stat.Ctimespec.Nsec, 10)
}
