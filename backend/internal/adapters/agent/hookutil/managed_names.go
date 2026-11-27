package hookutil

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// managedGitignoreReadLimit bounds how much of a marker is read. A larger file
// is rejected whole, so a truncated tail can never parse as a shorter name.
const managedGitignoreReadLimit = 64 * 1024

// ManagedNames are the entries an AO-managed .gitignore claims in its own
// directory: literal names, or globs such as "ao-standing-instructions-*.md".
type ManagedNames []string

// Match reports whether name, an entry of the marker's directory, is AO's.
func (m ManagedNames) Match(name string) bool {
	for _, pattern := range m {
		if pattern == name {
			return true
		}
		if ok, err := filepath.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

// ManagedNamesIn returns the entries an AO-managed .gitignore in dir claims.
// EnsureWorkspaceGitignore anchors every pattern to dir, and a listed
// directory is AO's whole, as git treats it in a project worktree. A missing,
// foreign, symlinked, oversized or unreadable marker claims nothing.
func ManagedNamesIn(dir string) ManagedNames {
	content, ok := ReadMarkerFile(filepath.Join(dir, ".gitignore"))
	if !ok || !strings.Contains(content, GitignoreSentinel) {
		return nil
	}
	var managed ManagedNames
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, "/"), "/")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		if _, err := filepath.Match(name, ""); err != nil {
			continue
		}
		managed = append(managed, name)
	}
	return managed
}

// restorableStateDirs are workspace-relative directories an AO .gitignore
// lists that hold an agent's own conversation, which a later restore reads
// back. They stay out of git status like any hook file, but they are never
// AO's to delete: a scratch folder holding one with files in it is kept.
var restorableStateDirs = []string{
	// memcode's native transcripts (.memcode/sessions/<id>/messages.json),
	// which its restore probe requires.
	".memcode/sessions",
}

// IsRestorableStateDir reports whether rel, slash-separated and relative to
// the workspace root, holds an agent's restorable conversation state.
func IsRestorableStateDir(rel string) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	for _, dir := range restorableStateDirs {
		if rel == dir {
			return true
		}
	}
	return false
}

// ReadMarkerFile reads a regular file of at most
// managedGitignoreReadLimit bytes without following a symlink, so a marker is
// only trusted from a file inside the workspace.
func ReadMarkerFile(file string) (string, bool) {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	handle, err := os.Open(file) //nolint:gosec // callers pass a path inside their own workspace
	if err != nil {
		return "", false
	}
	defer func() { _ = handle.Close() }()
	data, err := io.ReadAll(io.LimitReader(handle, managedGitignoreReadLimit+1))
	if err != nil || len(data) > managedGitignoreReadLimit {
		return "", false
	}
	return string(data), true
}
