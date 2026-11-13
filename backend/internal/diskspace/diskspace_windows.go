//go:build windows

package diskspace

import "golang.org/x/sys/windows"

// Free returns the bytes the current user can still write on the volume holding
// path.
func Free(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(name, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
