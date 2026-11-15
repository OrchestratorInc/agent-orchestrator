//go:build darwin

package sessionmanager

import "golang.org/x/sys/unix"

func cloneTree(src, dst string) error {
	err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if err == unix.ENOTSUP || err == unix.EXDEV {
		return errCloneUnsupported
	}
	return err
}
