package hookutil

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

// ErrRootFileTooLarge is returned by ReadRootFile when the file exceeds the
// caller's size cap.
var ErrRootFileTooLarge = errors.New("file is too large")

// gitignoreMaxBytes bounds reading an agent-writable .gitignore. A larger file
// cannot be AO's own (a few lines), so it is treated as foreign and left
// untouched, the same safe degradation as a .gitignore without the sentinel.
const gitignoreMaxBytes = 1 << 20

// The helpers below confine access to an agent-writable workspace through an
// os.Root opened on it (the trust boundary): a symlink planted at the file or at
// any parent directory can neither be read through nor make AO create or
// replace files outside the root. A parent directory may be a symlink to a
// directory inside the root (os.Root resolves it there). A non-regular entry
// (symlink, FIFO) in place of the file itself is treated as absent and
// replaced on write.

func parentDirs(rel string) []string {
	var dirs []string
	dir := filepath.Dir(rel)
	for dir != "." && dir != string(filepath.Separator) {
		dirs = append([]string{dir}, dirs...)
		dir = filepath.Dir(dir)
	}
	return dirs
}

// isInRootDir reports whether dir is a directory once os.Root resolves it.
// os.Root follows a symlink only while it stays inside the root, so a link to
// an in-repo directory (a repository that commits .claude -> config/claude)
// counts, and a link that escapes, dangles or names a file does not.
func isInRootDir(root *os.Root, dir string) bool {
	fi, err := root.Stat(dir)
	return err == nil && fi.IsDir()
}

// ParentsAreDirs reports whether every parent directory of rel resolves to a
// directory inside root: a real directory or a symlink to one in the root.
// It never modifies anything.
func ParentsAreDirs(root *os.Root, rel string) (bool, error) {
	for _, dir := range parentDirs(rel) {
		fi, err := root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() && !isInRootDir(root, dir) {
			return false, nil
		}
	}
	return true, nil
}

// EnsureParentDirs makes every parent directory of rel resolve to a directory
// inside root, creating missing ones (0750). A symlink to a directory inside
// the root is kept: os.Root resolves it without leaving the root, and every
// later access through root refuses a link swapped in that escapes. A symlink
// that escapes or dangles, or a file, squatting on a component is removed (the
// entry itself, never its target, never recursively) and replaced.
func EnsureParentDirs(root *os.Root, rel string) error {
	for _, dir := range parentDirs(rel) {
		fi, err := root.Lstat(dir)
		switch {
		case err == nil && fi.IsDir():
			continue
		case err == nil && fi.Mode()&fs.ModeSymlink != 0 && isInRootDir(root, dir):
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

// ReadRootFile returns the contents of rel when it is a regular file under
// parent directories that resolve inside root, capped at maxBytes
// (ErrRootFileTooLarge beyond it). present is false for anything else, which
// callers treat as absent.
func ReadRootFile(root *os.Root, rel string, maxBytes int64) (data []byte, present bool, err error) {
	ok, err := ParentsAreDirs(root, rel)
	if err != nil || !ok {
		return nil, false, err
	}
	lfi, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !lfi.Mode().IsRegular() {
		return nil, false, nil
	}
	// Non-blocking so a FIFO swapped in after the Lstat cannot hang the open.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	// The opened file must be the one Lstat saw, not a link swapped in since.
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || !os.SameFile(lfi, fi) {
		return nil, false, nil
	}
	data, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > maxBytes {
		return nil, false, ErrRootFileTooLarge
	}
	return data, true, nil
}

// WriteRootFile atomically replaces rel with data: a 0600 temp file in the same
// directory is renamed over the target, which replaces a symlink entry rather
// than following it. An empty directory squatting on the target is removed
// first; a non-empty one is an error. The temp file is removed on failure.
func WriteRootFile(root *os.Root, rel string, data []byte) (err error) {
	if err := EnsureParentDirs(root, rel); err != nil {
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

// EnsureGitignoreInRoot is EnsureWorkspaceGitignore confined to root: relDir
// (root-relative, "." for the root itself) is created or repaired as a real
// directory, and its .gitignore is read and written only through root. A
// non-regular .gitignore (symlink, FIFO) is treated as absent and replaced; one
// over 1 MiB is treated as foreign and left untouched.
func EnsureGitignoreInRoot(root *os.Root, relDir string, names ...string) error {
	rel := filepath.Join(relDir, ".gitignore")
	existing, present, err := ReadRootFile(root, rel, gitignoreMaxBytes)
	if errors.Is(err, ErrRootFileTooLarge) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	if present && !strings.Contains(string(existing), GitignoreSentinel) {
		return nil
	}
	var b strings.Builder
	b.WriteString(GitignoreSentinel)
	b.WriteString("\n/.gitignore\n")
	for _, name := range names {
		b.WriteString("/")
		b.WriteString(filepath.ToSlash(name))
		b.WriteString("\n")
	}
	if err := WriteRootFile(root, rel, []byte(b.String())); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}
