//go:build darwin

package storagemgr

import (
	"errors"

	"golang.org/x/sys/unix"
)

// cloneTree clones src (a file or a whole directory hierarchy) to dst with
// APFS clonefile: one call, metadata only, blocks shared until written.
func cloneTree(src, dst string) error {
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) {
			return errCloneUnsupported
		}
		return err
	}
	return nil
}
