//go:build windows

package persistenthost

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientHostFileBusy reports Windows lock races where another process
// still holds host.json (or its rename target) after the owner has begun to
// exit. ERROR_ACCESS_DENIED is included because some AV scanners surface the
// same window that way.
func isTransientHostFileBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
