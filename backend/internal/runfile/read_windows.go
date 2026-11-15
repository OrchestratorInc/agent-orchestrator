//go:build windows

package runfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Polling must not deny the daemon's deletion handles. os.Open omits
// FILE_SHARE_DELETE and can make either side fail during shutdown.
func openRead(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(extendedReadPath(path))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	for attempt := 0; ; attempt++ {
		handle, err := windows.CreateFile(name, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return os.NewFile(uintptr(handle), path), nil
		}
		// Windows may deny a new open while deletion is pending on another
		// handle. Let that brief transition finish, but preserve persistent
		// permission/sharing errors rather than reporting a missing daemon.
		if attempt == 4 || !(errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_DELETE_PENDING)) {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Preserve os.Open's long-path support when calling the Windows API directly.
// The extended prefix requires an absolute, normalized path. Leave device and
// already-prefixed paths unchanged, as the standard library does.
func extendedReadPath(path string) string {
	if strings.HasPrefix(path, `\??\`) ||
		len(path) >= 4 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) &&
			(path[2] == '?' || path[2] == '.') && os.IsPathSeparator(path[3]) {
		return path
	}
	full, err := filepath.Abs(path)
	if err != nil || len(full) < 248 {
		return path
	}
	if strings.HasPrefix(full, `\\`) {
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}
