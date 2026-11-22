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

// Unlike os.Open, share deletion so shutdown/replacement and polling do not
// deny each other's handles. This matches the chat host descriptor reader.
func openRunFile(path string) (*os.File, error) {
	name := path
	// Match os.Open's long-path support. Extended paths must be absolute and
	// normalized; leave explicit device/extended paths alone.
	if !strings.HasPrefix(path, `\??\`) && !(len(path) >= 4 &&
		os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) &&
		(path[2] == '?' || path[2] == '.') && os.IsPathSeparator(path[3])) {
		if full, err := filepath.Abs(path); err == nil && len(full) >= 248 {
			if strings.HasPrefix(full, `\\`) {
				name = `\\?\UNC\` + full[2:]
			} else {
				name = `\\?\` + full
			}
		}
	}
	pathp, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	for attempt := 0; ; attempt++ {
		handle, err := windows.CreateFile(pathp, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return os.NewFile(uintptr(handle), path), nil
		}
		// A pending delete rejects even share-delete readers until its final
		// handle closes. Retry briefly; never turn an unreadable file into
		// evidence that the daemon stopped or hide a persistent access error.
		if attempt >= 9 || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
