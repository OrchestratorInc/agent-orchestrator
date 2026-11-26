// Package storagemgr measures and bounds AO's disk footprint.
//
// It sizes everything AO keeps under its state dir, plans cleanups from
// durable facts with a pure policy (policy.go), and executes them: offloading
// reinstallable dependencies from quiet open sessions, removing worktrees of
// ended sessions through the no-force session cleanup, aging out backups,
// logs, dev profiles and dependency templates, and returning free database
// pages to the disk. It never touches anything outside the AO state dir.
package storagemgr

import (
	"path/filepath"
)

// Layout locates AO's data. StateDir is ~/.ao (or the AO_DATA_DIR override);
// DataDir holds the database and worktrees.
type Layout struct {
	StateDir string
	DataDir  string
}

// Worktrees is the managed worktree root, one folder per project.
func (l Layout) Worktrees() string { return filepath.Join(l.DataDir, "worktrees") }

// Database is the SQLite database file.
func (l Layout) Database() string { return filepath.Join(l.DataDir, "ao.db") }

// Harnesses holds AO-downloaded agent CLIs.
func (l Layout) Harnesses() string { return filepath.Join(l.DataDir, "harnesses") }

// Templates holds dependency trees kept for copy-on-write restores.
func (l Layout) Templates() string { return filepath.Join(l.DataDir, "dep-templates") }

// Trash holds trees moved aside by an offload or cleanup and not yet deleted.
// It sits on the same volume as the worktrees so the move is a rename.
func (l Layout) Trash() string { return filepath.Join(l.DataDir, "storage-trash") }

// Electron is the desktop's Chromium profile.
func (l Layout) Electron() string { return filepath.Join(l.StateDir, "electron") }

// Logs is the desktop's log directory.
func (l Layout) Logs() string { return filepath.Join(l.StateDir, "logs") }

// DevProfiles holds isolated data dirs from AO dev builds. They exist only in
// the default state dir, never under an AO_DATA_DIR override.
func (l Layout) DevProfiles() string { return filepath.Join(l.StateDir, "dev") }

// LooseLogFiles are single log files kept beside the state and data dirs.
func (l Layout) LooseLogFiles() []string {
	return []string{filepath.Join(l.StateDir, "daemon.log"), filepath.Join(l.DataDir, "hooks.log")}
}

// Within reports whether path resolves inside root. Both are resolved through
// symlinks where they exist, so a link inside root cannot point a delete
// outside it.
func Within(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = filepath.Clean(root)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// A path that does not exist yet (or a dangling link) is judged by its
		// parent, which must exist and resolve inside root.
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		if parentErr != nil {
			return false
		}
		resolvedPath = filepath.Join(parent, filepath.Base(path))
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !hasParentPrefix(rel)
}

func hasParentPrefix(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && (rel[2] == '/' || rel[2] == '\\')
}
