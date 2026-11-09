//go:build windows

package session

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// resolvedFilesystemPath 从固定文件句柄解析真实路径，按系统所需长度扩容并保留 UNC 语义。
func resolvedFilesystemPath(path string) (string, error) {
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
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], size, 0)
		if err != nil {
			return "", err
		}
		if n < size {
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
