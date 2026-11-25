//go:build darwin

package skillbridge

import "golang.org/x/sys/unix"

// cloneFile copies a file with APFS copy-on-write, which takes no extra disk.
func cloneFile(src, dst string) error { return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) }
