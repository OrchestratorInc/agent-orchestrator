//go:build linux

package skillbridge

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// defaultTotalBytes bounds one session's copies. A reflink takes no extra disk,
// but only some file systems (Btrfs, XFS) have them; on the rest each session
// holds full copies, so the total stays small.
const defaultTotalBytes = 64 << 20

// cloneFile copies a file as a reflink, which shares its blocks until either
// side changes. Where the file system has none, it leaves nothing behind, so
// the caller copies the bytes.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		return errors.Join(err, out.Close(), os.Remove(dst))
	}
	return out.Close()
}
