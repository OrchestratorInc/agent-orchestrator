//go:build linux

package storagemgr

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneTree recreates src at dst, cloning every regular file with the FICLONE
// reflink ioctl (Btrfs, XFS and other reflink filesystems). Directories and
// symlinks are recreated; any file that cannot be cloned aborts the whole
// tree with errCloneUnsupported.
func cloneTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.IsDir():
			return os.Mkdir(target, info.Mode().Perm()|0o700)
		case entry.Type().IsRegular():
			return cloneFile(path, target, info.Mode().Perm())
		default:
			// Sockets, fifos and devices have no content to clone.
			return nil
		}
	})
}

func cloneFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil { //nolint:gosec // file descriptors fit in int
		_ = out.Close()
		_ = os.Remove(dst)
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EINVAL) ||
			errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.ENOSYS) {
			return errCloneUnsupported
		}
		return err
	}
	return out.Close()
}
