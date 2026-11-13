package sessionartifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A turn's artifacts are listed in turnsDir, one file per turn, with their
// content kept in keptDir. The version store forgets a renamed file's old path
// and keeps only a file's latest versions; what a turn kept stays, so a fork of
// an earlier turn still gets every artifact its conversation names.
var (
	turnsDir = filepath.Join(VersionsDir, "turns")
	keptDir  = filepath.Join(VersionsDir, "kept")
)

var turnIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type turnArtifacts struct {
	// Files maps each artifact's path to the sha256 of its content.
	Files map[string]string `json:"files"`
	// KeptAt orders the lists, for keeping only the latest.
	KeptAt time.Time `json:"keptAt"`
}

// TurnArtifacts returns the artifacts turnID started with in the artifact
// directory dir, each path mapped to the sha256 of its content, and whether
// that turn's list was kept.
func TurnArtifacts(dir, turnID string) (map[string]string, bool, error) {
	if !turnIDPattern.MatchString(turnID) {
		return nil, false, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, turnsDir, turnID+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var list turnArtifacts
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, false, fmt.Errorf("read artifacts of turn %s: %w", turnID, err)
	}
	return list.Files, true, nil
}

// WriteTurnArtifacts records files as the artifacts turnID started with, at
// keptAt.
func WriteTurnArtifacts(dir, turnID string, files map[string]string, keptAt time.Time) error {
	if !turnIDPattern.MatchString(turnID) {
		return fmt.Errorf("bad turn id %q", turnID)
	}
	data, err := json.Marshal(turnArtifacts{Files: files, KeptAt: keptAt.UTC()})
	if err != nil {
		return err
	}
	folder := filepath.Join(dir, turnsDir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(folder, ".turn-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(folder, turnID+".json"))
}

// PruneTurnArtifacts keeps the lists of only the keep most recently kept
// turns in the artifact directory dir, and removes the kept content no
// remaining list names.
func PruneTurnArtifacts(dir string, keep int) error {
	entries, err := os.ReadDir(filepath.Join(dir, turnsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type kept struct {
		path string
		list turnArtifacts
	}
	var lists []kept
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, turnsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var list turnArtifacts
		if json.Unmarshal(data, &list) != nil {
			// Unreadable, so no fork could use it.
			_ = os.Remove(path)
			continue
		}
		lists = append(lists, kept{path: path, list: list})
	}
	sort.Slice(lists, func(i, j int) bool { return lists[i].list.KeptAt.After(lists[j].list.KeptAt) })
	var errs []error
	used := map[string]bool{}
	for i, l := range lists {
		if i >= keep {
			err := os.Remove(l.path)
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				continue
			}
			// Still on disk, so a fork may still read it: its content stays.
			errs = append(errs, err)
		}
		for _, sum := range l.list.Files {
			used[sum] = true
		}
	}
	// Removed through a root, so a symlink inside can't point a removal
	// outside the kept content.
	root, err := os.OpenRoot(filepath.Join(dir, keptDir))
	if errors.Is(err, fs.ErrNotExist) {
		return errors.Join(errs...)
	}
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() && !used[entry.Name()] {
			if err := root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// KeptBlobPath is where a turn's artifact content with sha256 sum is kept in
// the artifact directory dir.
func KeptBlobPath(dir, sum string) string {
	return filepath.Join(dir, keptDir, sum[:2], sum)
}
