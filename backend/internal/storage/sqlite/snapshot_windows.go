//go:build windows

package sqlite

import (
	"errors"

	"golang.org/x/sys/windows"
)

// cloneFile is unavailable: NTFS has no copy-on-write file clone, so Windows
// always takes the full-copy fallback.
func cloneFile(string, string) error {
	return errors.ErrUnsupported
}

// freeBytes is the space the calling user can still use on dir's volume.
func freeBytes(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
