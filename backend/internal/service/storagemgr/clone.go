package storagemgr

import (
	"errors"
	"os"
	"path/filepath"
)

// errCloneUnsupported means the filesystem cannot clone copy-on-write. Callers
// fall back to the project's Setup; a clone is never replaced by a full copy,
// which would cost the disk space cloning exists to save.
var errCloneUnsupported = errors.New("copy-on-write cloning is not supported here")

// cloneTreeAtomic clones src to dst through a temporary sibling and renames it
// into place, so dst is either absent or complete.
func cloneTreeAtomic(src, dst string) error {
	if pathExists(dst) {
		return errors.New("clone destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp := dst + ".ao-clone-" + newTrashName("tmp")
	if err := cloneTree(src, tmp); err != nil {
		_ = removeTree(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = removeTree(tmp)
		return err
	}
	return nil
}

// probeClone reports whether dir's filesystem clones copy-on-write, by cloning
// a small scratch file.
func probeClone(dir string) bool {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false
	}
	src, err := os.CreateTemp(dir, ".clone-probe-*")
	if err != nil {
		return false
	}
	srcPath := src.Name()
	defer func() { _ = os.Remove(srcPath) }()
	if _, err := src.WriteString("ao clone probe"); err != nil {
		_ = src.Close()
		return false
	}
	if err := src.Close(); err != nil {
		return false
	}
	dst := srcPath + ".clone"
	defer func() { _ = os.Remove(dst) }()
	return cloneTree(srcPath, dst) == nil
}
