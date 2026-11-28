//go:build darwin

package skillbridge

import "golang.org/x/sys/unix"

// defaultTotalBytes bounds one session's copies. APFS clones take no extra
// disk, so the bound is only against a runaway folder.
const defaultTotalBytes = 256 << 20

// cloneFile copies a file with APFS copy-on-write, which takes no extra disk.
func cloneFile(src, dst string) error { return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) }
