package storagemgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// treeSize sums the sizes of the regular files under root without following
// symlinks. A missing root is empty; unreadable entries are skipped, so a
// partial size is still returned with no error.
func treeSize(ctx context.Context, root string) (int64, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return info.Size(), nil
		}
		return 0, nil
	}
	var total int64
	visited := 0
	walkErr := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			// Permission or vanished entry: skip it, keep walking.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		visited++
		if visited%4096 == 0 {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			// Yield on big trees so the walk never starves the daemon.
			runtime.Gosched()
		}
		if entry.Type().IsRegular() {
			if fi, infoErr := entry.Info(); infoErr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return total, walkErr
	}
	return total, nil
}

// fileEntry is one file or directory a policy may age out.
type fileEntry struct {
	Path    string
	Bytes   int64
	ModTime time.Time
}

// listEntries returns the direct children of dir, sized, newest first. A
// missing dir is empty.
func listEntries(ctx context.Context, dir string, match func(name string, isDir bool) bool) ([]fileEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		if match != nil && !match(entry.Name(), entry.IsDir()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		size, sizeErr := treeSize(ctx, path)
		if sizeErr != nil {
			return nil, sizeErr
		}
		out = append(out, fileEntry{Path: path, Bytes: size, ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// newTrashName returns a unique name for a tree moved into the trash.
func newTrashName(base string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", sanitizeName(base), time.Now().UnixNano())
	}
	return sanitizeName(base) + "-" + hex.EncodeToString(buf)
}

func sanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
	if len(name) > 48 {
		name = name[:48]
	}
	if name == "" || name == "." || name == ".." {
		return "item"
	}
	return name
}

// moveToTrash moves path into trashDir with one rename, so a reader never sees
// a half-deleted tree. When the rename cannot happen (another volume, a
// locked file on Windows), the tree is left in place and the error returned:
// the next pass tries again rather than deleting in place.
func moveToTrash(trashDir, path string) (string, error) {
	if err := os.MkdirAll(trashDir, 0o750); err != nil {
		return "", fmt.Errorf("create trash: %w", err)
	}
	dest := filepath.Join(trashDir, newTrashName(filepath.Base(path)))
	if err := os.Rename(path, dest); err != nil {
		return "", fmt.Errorf("move %s aside: %w", path, err)
	}
	return dest, nil
}

// removeTree deletes path. On Windows a read-only file blocks RemoveAll, so
// read-only bits are cleared first when the first attempt fails.
func removeTree(path string) error {
	err := os.RemoveAll(path)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	_ = filepath.WalkDir(path, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil && info.Mode().Perm()&0o200 == 0 {
			_ = os.Chmod(p, info.Mode().Perm()|0o200)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// pathExists reports whether path exists without following a final symlink.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
