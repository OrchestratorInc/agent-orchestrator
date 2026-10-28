package litmus

import (
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func privateHomeEnv(legDir string) map[string]string {
	home := filepath.Join(legDir, "home")
	return map[string]string{
		"HOME": home, "USERPROFILE": home,
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":    filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":     filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":    filepath.Join(home, ".local", "state"),
		"APPDATA":           filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":      filepath.Join(home, "AppData", "Local"),
		"npm_config_prefix": filepath.Join(home, ".local"),
		"npm_config_cache":  filepath.Join(home, ".cache", "npm"),
	}
}

// removeLegAliases removes only symlinks into this leg, regardless of an app's naming convention.
func removeLegAliases(dir string, roots []string) error {
	for _, root := range slices.Compact(roots) {
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			path := filepath.Join(root, entry.Name())
			target, err := os.Readlink(path)
			if err != nil {
				continue // another process may have removed its own alias
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			rel, err := filepath.Rel(dir, target)
			if err == nil && (rel == "." || filepath.IsLocal(rel)) {
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
		}
	}
	return nil
}

type homeRoot struct {
	Path  string
	Depth int
}

type homeSnapshot struct {
	Roots      []homeRoot
	Entries    map[string]int64 // names and mtimes only, never contents or symlink targets
	Errors     []string
	Unreadable []string
}

func (s *homeSnapshot) recordError(err error) {
	if errors.Is(err, fs.ErrPermission) {
		path := err.Error()
		var pe *fs.PathError
		if errors.As(err, &pe) {
			path = pe.Path
		}
		s.Unreadable = append(s.Unreadable, "unreadable: "+path)
		return
	}
	s.Errors = append(s.Errors, err.Error())
}

func ownerHomeRoots() []homeRoot {
	var roots []homeRoot
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, homeRoot{home, 2})
	}
	return append(roots, homeRoot{"/opt/homebrew/bin", 1}, homeRoot{"/opt/homebrew/Cellar", 1}, homeRoot{"/usr/local/bin", 1})
}

func snapshotHome(roots []homeRoot, exclude string) homeSnapshot {
	out := homeSnapshot{Roots: roots, Entries: map[string]int64{}}
	var walk func(string, int)
	walk = func(path string, depth int) {
		if path == exclude {
			return
		}
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err != nil {
			out.recordError(err)
			return
		}
		out.Entries[path] = info.ModTime().UnixNano()
		if !info.IsDir() || depth == 0 {
			return // do not follow symlinks
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			out.recordError(err)
			return
		}
		for _, entry := range entries {
			walk(filepath.Join(path, entry.Name()), depth-1)
		}
	}
	for _, root := range roots {
		walk(root.Path, root.Depth)
	}
	return out
}

func homeChanges(before, after homeSnapshot) []string {
	paths := map[string]int64{}
	maps.Copy(paths, before.Entries)
	maps.Copy(paths, after.Entries)
	var changes []string
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		old, had := before.Entries[path]
		now, has := after.Entries[path]
		switch {
		case !had:
			changes = append(changes, "+ "+path)
		case !has:
			changes = append(changes, "- "+path)
		case old != now:
			changes = append(changes, "~ "+path)
		}
	}
	return changes
}

// snapshotHomeBefore keeps the first launch's baseline across legs and daemon restarts.
// Observation failures are diagnostics and never stop a target.
func (s *Service) snapshotHomeBefore(id string) {
	s.mu.Lock()
	var before homeSnapshot
	err := s.store.readHomeSnapshot(id, &before)
	s.mu.Unlock()
	if err == nil {
		return
	}
	if !errors.Is(err, fs.ErrNotExist) {
		s.store.homeSnapshotFailure(id, err)
		return
	}
	before = snapshotHome(ownerHomeRoots(), s.store.dataDir())
	s.mu.Lock()
	defer s.mu.Unlock()
	// Another leg may have stored the baseline while this snapshot was walking HOME.
	var existing homeSnapshot
	if err := s.store.readHomeSnapshot(id, &existing); err == nil {
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		s.store.homeSnapshotFailure(id, err)
		return
	}
	if err := s.store.put(id, "home-before.json", before); err != nil {
		s.store.homeSnapshotFailure(id, err)
	}
}

const homeChangeNotice = "Any process on this Mac can cause these, including the tester and other agents. Litmus does not attribute them."

func (s *Store) readHomeSnapshot(id string, before *homeSnapshot) error {
	if err := s.read(id, "home-before.json", before); err != nil {
		return err
	}
	if before.Entries == nil || len(before.Roots) == 0 {
		return errors.New("home snapshot has no baseline")
	}
	return nil
}

func (s *Store) homeSnapshotFailure(id string, err error) {
	slog.Warn("litmus: home snapshot failed", "run", id, "error", err)
	if writeErr := s.WriteFile(id, "home-snapshot-errors.txt", []byte(err.Error()+"\n")); writeErr != nil {
		slog.Warn("litmus: home snapshot diagnostic failed", "run", id, "error", writeErr)
	}
}

func (s *Store) reportHomeChanges(id string) {
	var before homeSnapshot
	if err := s.readHomeSnapshot(id, &before); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.homeSnapshotFailure(id, err)
		}
		return
	}
	after := snapshotHome(before.Roots, s.dataDir())
	changes := homeChanges(before, after)
	text := homeChangeNotice + "\n" + strings.Join(changes, "\n")
	if len(changes) > 0 {
		text += "\n"
	}
	if err := s.WriteFile(id, "home-changes.txt", []byte(text)); err != nil {
		slog.Warn("litmus: home changes report failed", "run", id, "error", err)
	}
	diagnostics := append(slices.Clone(before.Errors), after.Errors...)
	diagnostics = append(diagnostics, before.Unreadable...)
	diagnostics = append(diagnostics, after.Unreadable...)
	slices.Sort(diagnostics)
	if diagnostics = slices.Compact(diagnostics); len(diagnostics) > 0 {
		if err := s.WriteFile(id, "home-snapshot-errors.txt", []byte(strings.Join(diagnostics, "\n")+"\n")); err != nil {
			slog.Warn("litmus: home snapshot diagnostics failed", "run", id, "error", err)
		}
	}
}

func homeEnvironment(run Run, runDir string) string {
	private, shared := false, false
	for _, leg := range run.targets() {
		owned, err := readOwned(filepath.Join(runDir, leg.Name))
		if err != nil || owned.PrivateHome == nil {
			continue
		}
		if *owned.PrivateHome {
			private = true
		} else {
			shared = true
		}
	}
	switch {
	case private && shared:
		return "legs ran with private HOME for $HOME-based state and with the owner's HOME; no owner provider state was copied in; provider login state was not checked"
	case private:
		return "legs ran with a private HOME for $HOME-based state; no owner provider state was copied in; provider login state was not checked"
	case shared:
		return "legs ran with the owner's HOME"
	default:
		return ""
	}
}

func homeSnapshotNote(runDir string) string {
	if data, err := os.ReadFile(filepath.Join(runDir, "home-snapshot-errors.txt")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" && !strings.HasPrefix(line, "unreadable: ") {
				return "Home changes may be incomplete; see home-snapshot-errors.txt."
			}
		}
	}
	return ""
}
