//go:build windows

package sessionmanager

import (
	"context"
	"math"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// ponytail: Windows relies on the provider refusing a second writer; add a
// LockFileEx probe if imported chats there report races.
func providerLockHeld(string) bool { return false }

// processStart reads a live process's executable name and start time.
func processStart(_ context.Context, pid int) (string, time.Time, bool) {
	if pid <= 0 || pid > math.MaxUint32 {
		return "", time.Time{}, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", time.Time{}, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", time.Time{}, false
	}
	var name [windows.MAX_LONG_PATH]uint16
	size := uint32(len(name))
	if err := windows.QueryFullProcessImageName(handle, 0, &name[0], &size); err != nil {
		return "", time.Time{}, false
	}
	return filepath.Base(windows.UTF16ToString(name[:size])), time.Unix(0, creation.Nanoseconds()), true
}
