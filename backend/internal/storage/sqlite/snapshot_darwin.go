//go:build darwin

package sqlite

import "golang.org/x/sys/unix"

// cloneFile makes dst a copy-on-write clone of src (APFS clonefile): no data is
// copied, so it costs neither time nor space until one of the two files
// changes. It fails when the volume cannot clone (HFS+, exFAT, network shares),
// when src and dst are on different volumes, and when dst exists.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}

// freeBytes is the space an unprivileged process can still use on dir's volume.
func freeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
