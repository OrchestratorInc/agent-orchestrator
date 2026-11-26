package sessionartifacts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrImportFileChanged reports a guarded file (domain.ArtifactImportFile.Guard)
// that no longer holds the content kept before the import: the agent wrote
// it in between, so the import leaves it, and every other file, as it was.
var ErrImportFileChanged = errors.New("the file changed while it was being replaced")

// ErrImportIncomplete reports an import that failed and could not be fully
// undone: the error names each file left changed and where its original is.
var ErrImportIncomplete = errors.New("the import could not be undone")

// SharedCopyMarker is the hidden file in a copy's folder that marks it as a
// copy of an artifact someone else shared: the desktop app refuses to publish
// anything under it. Hidden, so List leaves it out.
const SharedCopyMarker = ".ao-shared-copy"

// MarkSharedCopy writes SharedCopyMarker into copyDir, a clean folder inside dir.
func MarkSharedCopy(dir, copyDir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	name := filepath.Join(filepath.FromSlash(copyDir), SharedCopyMarker)
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// CleanImportPath returns p as the slash-separated path an imported file is
// written at, or false when p could leave the artifact directory or name
// something List leaves out: an absolute path, "..", a backslash, an empty
// segment, a hidden segment, or VersionsDir. A segment ending in a dot or a
// space is refused on every system: Windows drops it, so "a." and "a" would
// be one file there.
func CleanImportPath(p string) (string, bool) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return "", false
	}
	clean := path.Clean(p)
	if clean != p || clean == "." {
		return "", false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", false
		}
	}
	if !filepath.IsLocal(filepath.FromSlash(clean)) || Skips(clean) {
		return "", false
	}
	return clean, true
}

// WriteImport writes files into dir, creating the folders they are in and
// replacing a regular file already at a path, all or nothing: every file is
// first written to a temporary file beside it, and only then are they swapped
// in. If a swap fails (on Windows, another program holding a file open, or a
// guarded file the agent changed), the files already swapped are put back, so
// the folder is never left holding a mix of two versions. os.Root refuses
// ".." and symlinks that leave dir. Paths must already be clean
// (CleanImportPath).
func WriteImport(dir string, files []domain.ArtifactImportFile) error {
	if dir == "" {
		return fs.ErrNotExist
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	// What an earlier import stopped part way left is put right first, before
	// anything of this one is staged beside it.
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	recoverImport(root, paths)

	staged := make([]stagedImport, 0, len(files))
	defer func() {
		for _, file := range staged {
			if file.temp != "" {
				_ = root.Remove(file.temp)
			}
		}
	}()
	for _, file := range files {
		next, err := stageImportFile(root, file)
		if err != nil {
			return fmt.Errorf("import %s: %w", file.Path, err)
		}
		staged = append(staged, next)
	}

	for i := range staged {
		if err := swapIn(root, &staged[i]); err != nil {
			stranded := staged[i].left
			for j := i - 1; j >= 0; j-- {
				stranded = append(stranded, putBack(root, &staged[j])...)
			}
			if len(stranded) > 0 {
				// Not "nothing changed": say what is left, and never delete a backup,
				// which may be the only copy of what a file held.
				return fmt.Errorf("%w: import %s failed (%v) and could not undo %s", ErrImportIncomplete, filepath.ToSlash(staged[i].name), err, strings.Join(stranded, "; "))
			}
			return fmt.Errorf("import %s: %w", filepath.ToSlash(staged[i].name), err)
		}
	}
	for _, file := range staged {
		if file.backup != "" {
			_ = root.Remove(file.backup)
		}
	}
	return nil
}

// stagedImport is one file of an import on its way in: written to temp, then
// renamed to name, with the file it replaces kept at backup until the whole
// import is in.
type stagedImport struct {
	name, temp, backup string
	guard              bool
	kept               string
	// left is what swapIn could not undo of itself.
	left []string
}

func stageImportFile(root *os.Root, file domain.ArtifactImportFile) (stagedImport, error) {
	name := filepath.FromSlash(file.Path)
	if parent := filepath.Dir(name); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return stagedImport{}, err
		}
	}
	perm := fs.FileMode(0o644)
	info, err := root.Lstat(name)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return stagedImport{}, fmt.Errorf("not a regular file")
	case err == nil:
		perm = info.Mode().Perm()
	case !os.IsNotExist(err):
		return stagedImport{}, err
	}
	temp, err := siblingName(name, importTempKind)
	if err != nil {
		return stagedImport{}, err
	}
	out, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return stagedImport{}, err
	}
	_, err = out.Write(file.Data)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = root.Remove(temp)
		return stagedImport{}, err
	}
	return stagedImport{name: name, temp: temp, guard: file.Guard, kept: file.Kept}, nil
}

const (
	importTempKind   = "ao-import"
	importBackupKind = "ao-backup"
)

// RecoverImport tidies, for each clean path in dir, what an import stopped
// part way left beside it (see recoverFolder). Run it before keeping the
// files' current content as versions, so what is kept is what is there.
func RecoverImport(dir string, paths []string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	recoverImport(root, paths)
}

func recoverImport(root *os.Root, paths []string) {
	// Every folder the import writes into, so leftovers beside files this
	// version no longer has are tidied too, not only those it writes again.
	folders := map[string]bool{}
	for _, p := range paths {
		for parent := filepath.Dir(filepath.FromSlash(p)); ; parent = filepath.Dir(parent) {
			folders[parent] = true
			if parent == "." {
				break
			}
		}
	}
	for folder := range folders {
		recoverFolder(root, folder)
	}
}

// recoverFolder tidies what an import stopped part way (the daemon exiting
// mid-swap) left in folder: temporary files go, and a file that is missing
// comes back from its backup. A backup beside a file that is there is left
// alone: it may be the only copy of what that file held before.
func recoverFolder(root *os.Root, folder string) {
	dir, err := root.Open(folder)
	if err != nil {
		return
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		sibling := filepath.Join(folder, name)
		if original, ok := leftoverOf(name, importTempKind); ok && original != "" {
			_ = root.Remove(sibling)
			continue
		}
		original, ok := leftoverOf(name, importBackupKind)
		if !ok || original == "" {
			continue
		}
		target := filepath.Join(folder, original)
		if _, err := root.Lstat(target); os.IsNotExist(err) {
			_ = renameRetrying(root, sibling, target)
		}
	}
}

// leftoverOf is the file name a temporary or backup file named name was made
// for (siblingName): ".<original>.<kind>-<16 hex digits>".
func leftoverOf(name, kind string) (string, bool) {
	suffix := "." + kind + "-"
	at := strings.LastIndex(name, suffix)
	if !strings.HasPrefix(name, ".") || at < 1 || len(name)-at-len(suffix) != 16 {
		return "", false
	}
	if _, err := hex.DecodeString(name[at+len(suffix):]); err != nil {
		return "", false
	}
	return name[1:at], true
}

// swapIn moves the file at name aside to a backup, when there is one, and
// renames the staged file into its place. A guarded file is checked once it
// is aside, where the agent can no longer change it under the check.
func swapIn(root *os.Root, file *stagedImport) error {
	if _, err := root.Lstat(file.name); err == nil {
		backup, err := siblingName(file.name, importBackupKind)
		if err != nil {
			return err
		}
		if err := renameRetrying(root, file.name, backup); err != nil {
			return err
		}
		file.backup = backup
	} else if !os.IsNotExist(err) {
		return err
	}
	if file.guard {
		sum := ""
		if file.backup != "" {
			var err error
			if sum, err = fileSHA256(root, file.backup); err != nil {
				file.left = putBack(root, file)
				return err
			}
		}
		if sum != file.kept {
			file.left = putBack(root, file)
			return ErrImportFileChanged
		}
		// Written again while the original was aside (an agent or an editor's
		// autosave): that new file is the person's, not the import's to replace.
		if _, err := root.Lstat(file.name); err == nil {
			// The new file stays. The original it replaced matched what was
			// kept as a version, so its backup is not the only copy of it.
			if file.backup != "" && removeRetrying(root, file.backup) == nil {
				file.backup = ""
			}
			file.left = putBack(root, file)
			return ErrImportFileChanged
		}
	}
	if err := renameRetrying(root, file.temp, file.name); err != nil {
		file.left = putBack(root, file)
		return err
	}
	file.temp = ""
	if file.guard && file.backup != "" {
		// Written through a handle already open on the original while it was
		// checked: its content is no longer what was kept, so it goes back.
		if sum, err := fileSHA256(root, file.backup); err != nil || sum != file.kept {
			file.left = putBack(root, file)
			return ErrImportFileChanged
		}
	}
	return nil
}

// putBack undoes swapIn: the file it replaced returns, or a file it added
// goes. It returns what it could not undo, for the error to name.
func putBack(root *os.Root, file *stagedImport) []string {
	var left []string
	// temp is cleared once the new file is in place; until then name holds nothing of this import.
	if file.temp == "" && file.backup == "" {
		if err := removeRetrying(root, file.name); err != nil && !os.IsNotExist(err) {
			left = append(left, filepath.ToSlash(file.name)+" (added, not removed)")
		}
	}
	if file.backup != "" {
		// A rename over the new file replaces it on every system.
		if err := renameRetrying(root, file.backup, file.name); err == nil {
			file.backup = ""
		} else {
			left = append(left, filepath.ToSlash(file.name)+" (original kept at "+filepath.ToSlash(file.backup)+")")
		}
	}
	return left
}

// removeRetrying removes name, trying again for a moment on Windows, where a
// scanner briefly holding the file open makes it fail.
func removeRetrying(root *os.Root, name string) error {
	err := root.Remove(name)
	for attempt := 1; err != nil && !os.IsNotExist(err) && runtime.GOOS == "windows" && attempt <= 5; attempt++ {
		time.Sleep(time.Duration(attempt) * 20 * time.Millisecond)
		err = root.Remove(name)
	}
	return err
}

func fileSHA256(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// siblingName is a hidden name beside name, which the artifact list skips.
func siblingName(name, kind string) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(name), "."+filepath.Base(name)+"."+kind+"-"+hex.EncodeToString(suffix)), nil
}

// renameRetrying renames, trying again for a moment on Windows, where a
// scanner or indexer briefly holding a file open makes a rename fail.
func renameRetrying(root *os.Root, from, to string) error {
	err := renameInRoot(root, from, to)
	for attempt := 1; err != nil && runtime.GOOS == "windows" && attempt <= 5; attempt++ {
		time.Sleep(time.Duration(attempt) * 20 * time.Millisecond)
		err = renameInRoot(root, from, to)
	}
	return err
}

// renameInRoot is os.Root.Rename; a test replaces it to fail one rename.
var renameInRoot = func(root *os.Root, from, to string) error { return root.Rename(from, to) }
