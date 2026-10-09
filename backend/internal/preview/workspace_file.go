package preview

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// CleanWorkspacePath normalizes a browser/workspace path without discarding
// meaningful whitespace from valid Unix filenames. The leading slash used by
// browser requests is treated as the preview root, not as a host filesystem
// root.
func CleanWorkspacePath(raw string) (string, bool) {
	raw = strings.ReplaceAll(raw, `\`, "/")
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := strings.TrimPrefix(path.Clean("/"+raw), "/")
	if clean == "" || clean == "." {
		return "", false
	}
	return clean, true
}

// OpenWorkspaceFile opens a regular file beneath workspacePath using os.Root.
// os.Root follows symlinks that remain inside the workspace and rejects links
// that escape it, so callers can safely serve the returned handle without a
// second path lookup. Anything but a regular file is fs.ErrNotExist.
func OpenWorkspaceFile(workspacePath, assetPath string) (*os.File, fs.FileInfo, string, error) {
	clean, ok := CleanWorkspacePath(assetPath)
	if !ok {
		return nil, nil, "", fs.ErrNotExist
	}
	root, err := os.OpenRoot(workspacePath)
	if err != nil {
		return nil, nil, "", err
	}
	defer func() { _ = root.Close() }()

	// O_NONBLOCK: opening a FIFO would otherwise block until a writer appears,
	// and the path can be swapped for one at any time. A regular file's reads
	// ignore it. What was opened is checked on the open file.
	file, err := root.OpenFile(filepath.FromSlash(clean), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, "", err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, "", err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, "", fs.ErrNotExist
	}
	return file, info, clean, nil
}

// EntryAtPath resolves an existing regular workspace file with the same rooted
// confinement used by the HTTP serving path.
func EntryAtPath(workspacePath, assetPath string) (Entry, bool) {
	file, info, clean, err := OpenWorkspaceFile(workspacePath, assetPath)
	if err != nil {
		return Entry{}, false
	}
	_ = file.Close()
	absPath, _ := ConfinedPath(workspacePath, clean)
	return Entry{Path: clean, AbsPath: absPath, ModTime: info.ModTime(), Size: info.Size()}, true
}
