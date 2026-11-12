//go:build windows

package project

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// resolveDirectoryIdentity resolves a directory's identity on Windows, where a
// directory junction is not a symlink: filepath.EvalSymlinks returns a junction
// path unchanged, so a junction and its target would be treated as two different
// roots. Opening the directory and asking the filesystem for its final path
// resolves both spellings to the one directory they address.
//
// The handle-based query needs no special privilege (unlike creating a symlink),
// which matters because junctions are the mechanism users without Developer Mode
// have. If it fails for a path that EvalSymlinks can resolve, the symlink answer is
// used, so an unusual filesystem never turns a valid root into an error.
func resolveDirectoryIdentity(path string) (string, error) {
	resolved, handleErr := finalPathByHandle(path)
	if handleErr == nil {
		return resolved, nil
	}
	if viaSymlinks, err := filepath.EvalSymlinks(path); err == nil {
		return viaSymlinks, nil
	}
	return "", fmt.Errorf("resolve %q: %w", path, handleErr)
}

// finalPathByHandle opens a directory and asks Windows for its final path.
func finalPathByHandle(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	pointer, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		// FILE_FLAG_BACKUP_SEMANTICS is required to open a DIRECTORY handle.
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	buffer := make([]uint16, windows.MAX_PATH)
	size, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if size >= uint32(len(buffer)) {
		buffer = make([]uint16, size+1)
		size, err = windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
	}
	final := windows.UTF16ToString(buffer[:size])
	return filepath.Clean(trimExtendedPathPrefix(final)), nil
}

// trimExtendedPathPrefix removes the \\?\ (or \\?\UNC\) verbatim prefix that
// GetFinalPathNameByHandle returns, so the result compares with ordinary paths.
func trimExtendedPathPrefix(path string) string {
	const uncPrefix = `\\?\UNC\`
	const prefix = `\\?\`
	if len(path) >= len(uncPrefix) && path[:len(uncPrefix)] == uncPrefix {
		return `\\` + path[len(uncPrefix):]
	}
	if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
		return path[len(prefix):]
	}
	return path
}
