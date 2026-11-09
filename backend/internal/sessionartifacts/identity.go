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

// MaxPreviousPaths bounds how many earlier paths an artifact remembers.
const MaxPreviousPaths = 20

// Observation is what one scan saw in a session's artifact directory: its
// files as List returns them, with Registered added for registered paths the
// walk did not reach, and the identity of each file and bundle directory.
type Observation struct {
	Files []domain.SessionArtifactFile
	// quick holds what the walk's stat reported for each observed path. On
	// Linux it lacks the birth time, which only fullIdentity reads.
	quick map[string]domain.FileIdentity
	dir   string
}

// Observe lists dir as ListRegistered does and records each path's identity.
func Observe(ctx context.Context, dir string, registered []string) (Observation, error) {
	quick := make(map[string]domain.FileIdentity)
	files, err := listRegistered(ctx, dir, registered, quick)
	if err != nil {
		return Observation{}, err
	}
	observeBundles(dir, files, quick)
	return Observation{Files: files, quick: quick, dir: dir}, nil
}

// observeBundles adds the identity of each bundle directory that holds an
// observed file: a registered bundle is one artifact, renamed as one.
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

// Changes plans what the observation changes in rows, the session's artifact
// rows. Every observed path ends up with a row. A path whose file is not the
// one its row last saw, or that has no row, is matched against the rows whose
// file has left their path: a row whose file turns up at another path is a
// renamed artifact and moves there with its version history. Anything else at
// a path is a new artifact, or the same one written again in place.
//
// A rename never takes a path from an artifact whose file is still there: a
// file moved over another replaces its content, so the artifact at that path
// keeps its identity. It does take the path of an artifact whose file was gone
// before, whose row and history are dropped.
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
			changes.SetFile = append(changes.SetFile, domain.SessionArtifactFileUpdate{ID: row.ID, Path: row.Path})
		}
		return changes
	}
	sort.Strings(candidates)
	full := make(map[string]domain.FileIdentity, len(candidates))
	for _, path := range candidates {
		full[path] = o.fullIdentity(path)
	}

	// A row is a rename source when its file left its path: the path is gone,
	// or holds another file now.
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
	// Drop matches into a path whose artifact stays, until only moves remain
	// whose destination is free, held by a gone file, or being vacated.
	for changed := true; changed; {
		changed = false
		moving := make(map[string]bool, len(matches))
		for _, source := range matches {
			moving[source.ID] = true
		}
		for path := range matches {
			if row, ok := byPath[path]; ok && row.File.Ino != 0 && !moving[row.ID] {
				delete(matches, path)
				changed = true
			}
		}
	}

	moved := make(map[string]bool, len(matches))
	for _, path := range candidates {
		source, ok := matches[path]
		if !ok {
			continue
		}
		moved[source.ID] = true
		changes.Move = append(changes.Move, domain.SessionArtifactMove{
			ID: source.ID, From: source.Path, To: path, File: full[path],
			PreviousPaths: previousPaths(source.PreviousPaths, source.Path, path),
		})
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
			changes.SetFile = append(changes.SetFile, domain.SessionArtifactFileUpdate{ID: row.ID, Path: row.Path})
		}
	}
	return changes
}

// goneRows returns the rows that last saw a file at a path that now holds
// nothing. The walk stops at MaxFiles, so a path it did not reach is checked
// on disk before it counts as gone.
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

// addSource indexes row by its file, when that file is identified well enough
// to be recognized at another path.
func addSource(sources map[domain.FileIdentity]domain.SessionArtifactRecord, row domain.SessionArtifactRecord) {
	if row.File.SameFile(row.File) {
		sources[row.File] = row
	}
}

// sameQuick reports whether quick, which may lack the birth time and
// generation, names the file stored. The modification time counts only where
// it is part of the stored identity.
func sameQuick(stored, quick domain.FileIdentity) bool {
	if stored.Dev != quick.Dev || stored.Ino != quick.Ino || (stored.ModTime != 0 && stored.ModTime != quick.ModTime) {
		return false
	}
	return stored.Birth == 0 || quick.Birth == 0 || stored.Birth == quick.Birth
}

// previousPaths is previous with from added as the latest, and to removed in
// case the artifact moves back to a path it had.
func previousPaths(previous []string, from, to string) []string {
	out := slices.DeleteFunc(slices.Clone(previous), func(path string) bool { return path == from || path == to })
	out = append(out, from)
	if len(out) > MaxPreviousPaths {
		out = out[len(out)-MaxPreviousPaths:]
	}
	return out
}

// Apply sets ID, Registered and PreviousPaths on each observed file from rows,
// the session's artifact rows after its changes. A file inside a registered
// bundle directory is registered with it.
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

// RegisteredPaths returns the paths of the registered rows.
func RegisteredPaths(rows []domain.SessionArtifactRecord) []string {
	var paths []string
	for _, row := range rows {
		if row.RegisteredAt != nil {
			paths = append(paths, row.Path)
		}
	}
	return paths
}
