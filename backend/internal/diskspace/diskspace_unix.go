//go:build !windows

package diskspace

import "golang.org/x/sys/unix"

// Free returns the bytes an unprivileged process can still write on the volume
// holding path.
func Free(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil //nolint:gosec // Bsize is a positive block size.
}
