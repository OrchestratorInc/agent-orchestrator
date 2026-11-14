package preview

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

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

func EntryAtPath(workspacePath, assetPath string) (Entry, bool) {
	file, info, clean, err := OpenWorkspaceFile(workspacePath, assetPath)
	if err != nil {
		return Entry{}, false
	}
	_ = file.Close()
	absPath, _ := ConfinedPath(workspacePath, clean)
	return Entry{Path: clean, AbsPath: absPath, ModTime: info.ModTime(), Size: info.Size()}, true
}
