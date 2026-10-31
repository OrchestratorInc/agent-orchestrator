//go:build linux

package sqlite

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile makes dst a reflink clone of src (FICLONE) on filesystems that
// support it (btrfs, XFS with reflink, ...): no data is copied, so it costs
// neither time nor space until one of the two files changes. It fails on
// filesystems that cannot (ext4, tmpfs), when src and dst are on different
// filesystems, and when dst exists; a failed attempt leaves no dst behind.
func cloneFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	return unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
}

// freeBytes is the space an unprivileged process can still use on dir's volume.
func freeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:gosec // a filesystem block size is positive
}
