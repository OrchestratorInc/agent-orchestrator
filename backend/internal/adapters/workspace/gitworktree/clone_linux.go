//go:build linux

package gitworktree

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile makes destination a reflink of source on filesystems that share
// blocks (Btrfs, XFS, bcachefs). destination must not exist yet; a failed
// clone leaves nothing behind.
func cloneFile(source, destination string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	return unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
}
