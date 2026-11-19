//go:build darwin

package gitworktree

import "golang.org/x/sys/unix"

// cloneFile makes destination an APFS copy-on-write clone of source.
// destination must not exist yet.
func cloneFile(source, destination string) error {
	return unix.Clonefile(source, destination, unix.CLONE_NOFOLLOW)
}
