//go:build windows

package storagemgr

import (
	"golang.org/x/sys/windows"
)

// diskSpace reports the total and available bytes of the volume holding path.
// Available honors per-user quotas, like the free space Explorer shows.
func diskSpace(path string) (total, free uint64, err error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var freeAvailable, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvailable, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return totalBytes, freeAvailable, nil
}
