//go:build windows

package fspath

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolved is the path with every symlink followed, as the filesystem reads
// it. The path must exist.
func Resolved(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	pathp, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathp,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	size := uint32(256)
	for {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			return filepath.Clean(trimWindowsFinalPathPrefix(windows.UTF16ToString(buf[:n]))), nil
		}
		size = n + 1
	}
}

func trimWindowsFinalPathPrefix(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}
