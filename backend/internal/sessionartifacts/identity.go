package sessionartifacts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const MaxPreviousPaths = 20

type Observation struct {
	Files []domain.SessionArtifactFile
	quick map[string]domain.FileIdentity
	dir   string
}

func Observe(ctx context.Context, dir string, registered []string) (Observation, error) {
	quick := make(map[string]domain.FileIdentity)
	files, err := listRegistered(ctx, dir, registered, quick)
	if err != nil {
		return Observation{}, err
	}
	observeBundles(dir, files, quick)
	return Observation{Files: files, quick: quick, dir: dir}, nil
}

func observeBundles(dir string, files []domain.SessionArtifactFile, quick map[string]domain.FileIdentity) {
	for _, file := range files {
		for i := 0; i < len(file.Path); i++ {
			if file.Path[i] != '/' {
				continue
			}
			parent := file.Path[:i]
			if _, seen := quick[parent]; seen || !IsBundleDir(parent) {
				continue
			}
			if info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(parent))); err == nil && info.IsDir() {
				quick[parent] = quickIdentity(info)
			}
		}
	}
}

func (o Observation) Changes(rows []domain.SessionArtifactRecord, now time.Time, newID func() string) domain.SessionArtifactChanges {
	byPath := make(map[string]domain.SessionArtifactRecord, len(rows))
	for _, row := range rows {
		byPath[row.Path] = row
	}
	var candidates []string
	for path, quick := range o.quick {
		row, ok := byPath[path]
		if !ok || !sameQuick(row.File, quick) {
			candidates = append(candidates, path)
		}
	}
	var changes domain.SessionArtifactChanges
	gone := o.goneRows(rows)
	if len(candidates) == 0 {
		for _, row := range gone {
			forgetOrClear(&changes, row)
		}
		return changes
	}
	sort.Strings(candidates)
	full := make(map[string]domain.FileIdentity, len(candidates))
	for _, path := range candidates {
		full[path] = o.fullIdentity(path)
	}

	sources := make(map[domain.FileIdentity]domain.SessionArtifactRecord)
	for _, row := range gone {
		addSource(sources, row)
	}
	for _, path := range candidates {
		if row, ok := byPath[path]; ok {
			addSource(sources, row)
		}
	}
	matches := make(map[string]domain.SessionArtifactRecord)
	for _, path := range candidates {
		if source, ok := sources[full[path]]; ok && source.Path != path {
			matches[path] = source
		}
	}
	for changed := true; changed; {
		changed = false
		moving := make(map[string]bool, len(matches))
		for _, source := range matches {
			moving[source.ID] = true
		}
		for path := range matches {
			if row, ok := byPath[path]; ok && keepsItsPath(row) && !moving[row.ID] {
				delete(matches, path)
				changed = true
			}
		}
	}

	moved := make(map[string]bool, len(matches))
	for _, source := range matches {
		moved[source.ID] = true
	}
	for _, path := range candidates {
		source, ok := matches[path]
		if !ok {
			continue
		}
		move := domain.SessionArtifactMove{
			ID: source.ID, From: source.Path, To: path, File: full[path],
			PreviousPaths: previousPaths(source.PreviousPaths, source.Path, path),
		}
		changes.Move = append(changes.Move, move)
	}
	for _, path := range candidates {
		row, hasRow := byPath[path]
		switch _, renamedHere := matches[path]; {
		case renamedHere && hasRow && !moved[row.ID]:
			changes.Drop = append(changes.Drop, domain.SessionArtifactFileUpdate{ID: row.ID, Path: path})
		case renamedHere:
		case hasRow && !moved[row.ID]:
			changes.SetFile = append(changes.SetFile, domain.SessionArtifactFileUpdate{ID: row.ID, Path: path, File: full[path]})
		default:
			changes.Insert = append(changes.Insert, domain.SessionArtifactRecord{
				ID: newID(), Path: path, File: full[path], CreatedAt: now,
			})
		}
	}
	for _, row := range gone {
		if !moved[row.ID] {
			forgetOrClear(&changes, row)
		}
	}
	return changes
}

func keepsItsPath(row domain.SessionArtifactRecord) bool {
	return row.File.Ino != 0 || row.RegisteredAt != nil || row.HasVersions
}

func forgetOrClear(changes *domain.SessionArtifactChanges, row domain.SessionArtifactRecord) {
	update := domain.SessionArtifactFileUpdate{ID: row.ID, Path: row.Path}
	if row.RegisteredAt != nil {
		changes.SetFile = append(changes.SetFile, update)
		return
	}
	changes.Forget = append(changes.Forget, update)
}

func (o Observation) goneRows(rows []domain.SessionArtifactRecord) []domain.SessionArtifactRecord {
	var gone []domain.SessionArtifactRecord
	for _, row := range rows {
		if _, seen := o.quick[row.Path]; seen || row.File.Ino == 0 {
			continue
		}
		if _, err := os.Lstat(filepath.Join(o.dir, filepath.FromSlash(row.Path))); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, row)
		}
	}
	return gone
}

func (o Observation) fullIdentity(path string) domain.FileIdentity {
	full := filepath.Join(o.dir, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return o.quick[path]
	}
	return fullIdentity(full, info)
}

func addSource(sources map[domain.FileIdentity]domain.SessionArtifactRecord, row domain.SessionArtifactRecord) {
	if row.File.SameFile(row.File) {
		sources[row.File] = row
	}
}

func sameQuick(stored, quick domain.FileIdentity) bool {
	if stored.Dev != quick.Dev || stored.Ino != quick.Ino || (stored.ModTime != 0 && stored.ModTime != quick.ModTime) {
		return false
	}
	return stored.Birth == 0 || quick.Birth == 0 || stored.Birth == quick.Birth
}

func previousPaths(previous []string, from, to string) []string {
	out := slices.DeleteFunc(slices.Clone(previous), func(path string) bool { return path == from || path == to })
	out = append(out, from)
	if len(out) > MaxPreviousPaths {
		out = out[len(out)-MaxPreviousPaths:]
	}
	return out
}

func (o Observation) Apply(rows []domain.SessionArtifactRecord) []domain.SessionArtifactFile {
	byPath := make(map[string]domain.SessionArtifactRecord, len(rows))
	registered := make(map[string]bool)
	for _, row := range rows {
		byPath[row.Path] = row
		if row.RegisteredAt != nil {
			registered[row.Path] = true
		}
	}
	files := slices.Clone(o.Files)
	for i := range files {
		row := byPath[files[i].Path]
		files[i].ID = row.ID
		files[i].PreviousPaths = row.PreviousPaths
		files[i].Registered = namedBy(files[i].Path, registered)
	}
	return files
}

func RegisteredPaths(rows []domain.SessionArtifactRecord) []string {
	var paths []string
	for _, row := range rows {
		if row.RegisteredAt != nil {
			paths = append(paths, row.Path)
		}
	}
	return paths
}
