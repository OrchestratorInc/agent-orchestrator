//go:build !windows

package gitworktree

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func repairRemovePermissions(root string) bool {
	scoped, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer func() { _ = scoped.Close() }()

	repaired := false
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return fs.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return fs.SkipDir
		}
		const need = 0o700
		if info.Mode().Perm()&need == need {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fs.SkipDir
		}
		if chmodErr := scoped.Chmod(rel, info.Mode().Perm()|need); chmodErr == nil {
			repaired = true
		}
		return nil
	})
	return repaired
}

func isPermissionRemoveError(err error) bool { return errors.Is(err, fs.ErrPermission) }
