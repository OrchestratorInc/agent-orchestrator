//go:build darwin

package dirseed

import "golang.org/x/sys/unix"

// CloneTree clones a whole directory tree with one APFS clonefile call.
func CloneTree(src, dst string) error {
	err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if err == unix.ENOTSUP || err == unix.EXDEV {
		return ErrCloneUnsupported
	}
	return err
}
