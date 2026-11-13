//go:build darwin || linux

package storagemgr

import (
	"golang.org/x/sys/unix"
)

// diskSpace reports the total and available bytes of the volume holding path.
// Available counts only blocks an unprivileged process may use.
func diskSpace(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(st.Bsize) //nolint:gosec,unconvert // Bsize is positive; its type differs per OS.
	return st.Blocks * blockSize, st.Bavail * blockSize, nil
}
