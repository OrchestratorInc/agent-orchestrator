package hooksjson

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// hooksFileMaxBytes bounds reading an agent-writable settings file.
const hooksFileMaxBytes = 16 << 20

// The workspace is agent-writable, so every hooks file access goes through an
// os.Root opened on it (the trust boundary). A symlink planted by the agent or
// a cloned repository, at the file or at any parent directory, can therefore
// neither make AO read a host file into the worktree nor make it create or
// replace files outside the workspace. A symlink or other non-regular entry in
// place of the file is treated as absent and replaced on write; a symlink in
// place of a parent directory is removed (the link, never its target) and
// recreated as a real directory on write.

// openWorkspaceRoot opens workspacePath as a root and returns the hooks file's
// path relative to it. A hooks path outside the workspace is refused.
func openWorkspaceRoot(workspacePath, hooksPath string) (*os.Root, string, error) {
	rel, err := filepath.Rel(workspacePath, hooksPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("hooks path %s is outside workspace %s", hooksPath, workspacePath)
	}
	root, err := os.OpenRoot(workspacePath)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

// parentDirs lists the directory components of rel, outermost first, as
// root-relative paths ("a", "a/b" for "a/b/f.json").
func parentDirs(rel string) []string {
	var dirs []string
	dir := filepath.Dir(rel)
	for dir != "." && dir != string(filepath.Separator) {
		dirs = append([]string{dir}, dirs...)
		dir = filepath.Dir(dir)
	}
	return dirs
}

// parentsAreRealDirs reports whether every parent directory of rel exists as a
// real directory (not a symlink or file). It never modifies anything.
func parentsAreRealDirs(root *os.Root, rel string) (bool, error) {
	for _, dir := range parentDirs(rel) {
		fi, err := root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

// ensureParentDirs makes every parent directory of rel a real directory,
// removing a symlink or file squatting on a component first.
func ensureParentDirs(root *os.Root, rel string) error {
	for _, dir := range parentDirs(rel) {
		fi, err := root.Lstat(dir)
		switch {
		case err == nil && fi.IsDir():
			continue
		case err == nil:
			if err := root.Remove(dir); err != nil {
				return fmt.Errorf("remove non-directory %s: %w", dir, err)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		if err := root.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}

// readRootFile returns the contents of rel when it is a regular file under
// real parent directories. present is false for anything else, which the
// caller treats as absent.
func readRootFile(root *os.Root, rel string) (data []byte, present bool, err error) {
	ok, err := parentsAreRealDirs(root, rel)
	if err != nil || !ok {
		return nil, false, err
	}
	fi, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, false, nil
	}
	// Non-blocking so a FIFO swapped in after the Lstat cannot hang the open.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false, nil
	}
	data, err = io.ReadAll(io.LimitReader(f, hooksFileMaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > hooksFileMaxBytes {
		return nil, false, errors.New("file is too large")
	}
	return data, true, nil
}

// writeRootFile atomically replaces rel with data: a 0600 temp file in the
// same directory is renamed over the target, which replaces a symlink entry
// rather than following it. An empty directory squatting on the target is
// removed first; a non-empty one is an error.
func writeRootFile(root *os.Root, rel string, data []byte) (err error) {
	if err := ensureParentDirs(root, rel); err != nil {
		return err
	}
	if fi, lerr := root.Lstat(rel); lerr == nil && fi.IsDir() {
		if err := root.Remove(rel); err != nil {
			return fmt.Errorf("replace directory %s: %w", rel, err)
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(rel), ".ao-tmp-"+hex.EncodeToString(b[:]))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if cerr := f.Close(); cerr != nil {
		err = cerr
		return err
	}
	return root.Rename(tmp, rel)
}
