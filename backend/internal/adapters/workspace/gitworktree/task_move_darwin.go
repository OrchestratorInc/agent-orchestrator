package gitworktree

import "golang.org/x/sys/unix"

func moveTaskDirectory(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
