// Package sessionartifacts scans a session's artifact directory and
// classifies its durable output type. It only imports domain so both the
// session service (the read path) and the lifecycle reducer (the persist
// path) can depend on it without an import cycle.
package sessionartifacts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Dir returns the canonical artifact directory for a session under dataDir.
// It is a pure, deterministic path computation (dataDir + "artifacts" + id),
// so callers can derive the correct directory even for a session row whose
// stored ArtifactDir is empty — notably rows created before that column
// existed, which migration 0155 backfilled with ” rather than a real path.
func Dir(dataDir string, id domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "artifacts", string(id))
}

// VersionsDir is the directory inside a session's artifact directory that
// holds the artifacts' version history. List skips it, so snapshots are not
// artifacts themselves.
const VersionsDir = ".versions"

// MaxFiles bounds how many files List returns so a pathological artifact tree
// cannot stall a session read or the lifecycle reconcile that runs under the
// lifecycle mutex.
const MaxFiles = 1000

// MaxVisited bounds every entry List visits (directories and non-regular
// files included), so a tree of empty directories cannot stall a read.
const MaxVisited = 10000

// List walks a session's artifact directory and returns up to MaxFiles of its
// regular files outside VersionsDir, sorted by path. Entries matched by
// ignored are skipped, as are unreadable entries rather than failing the walk.
// A cancelled ctx stops the walk and returns its error.
func List(ctx context.Context, dir string) ([]domain.SessionArtifactFile, error) {
	return list(ctx, dir, nil)
}

// list is List that also records each file's identity in quick, when quick is
// not nil.
func list(ctx context.Context, dir string, quick map[string]domain.FileIdentity) ([]domain.SessionArtifactFile, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	files := make([]domain.SessionArtifactFile, 0)
	visited := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if len(files) >= MaxFiles || visited > MaxVisited {
			return filepath.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && ignored(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == VersionsDir && filepath.Dir(path) == root {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped, not fatal
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil //nolint:nilerr // an entry outside root is skipped, not fatal
		}
		rel = filepath.ToSlash(rel)
		files = append(files, artifactFile(rel, info))
		if quick != nil {
			quick[rel] = quickIdentity(info)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

// ignoredNames are OS metadata files and tool directories that List skips,
// matched case-insensitively.
var ignoredNames = map[string]bool{
	".ds_store":       true,
	".spotlight-v100": true,
	".trashes":        true,
	".fseventsd":      true,
	"thumbs.db":       true,
	"desktop.ini":     true,
	".git":            true,
	"node_modules":    true,
	"__pycache__":     true,
}

// ignored reports whether List skips an entry named name: ignoredNames and
// macOS "._" resource-fork files.
func ignored(name string) bool {
	return strings.HasPrefix(name, "._") || ignoredNames[strings.ToLower(name)]
}

// Skips reports whether List leaves out the entry at rel, a slash-separated
// path inside the artifact directory: anything under VersionsDir, or under or
// named by an ignored name at any depth.
func Skips(rel string) bool {
	parts := strings.Split(rel, "/")
	return parts[0] == VersionsDir || slices.ContainsFunc(parts, ignored)
}

// bundleSuffixes name directories that are one document on macOS. Keep this in
// step with BUNDLE_SUFFIXES in packages/shared/artifacts/deliverables.ts.
var bundleSuffixes = []string{".icon", ".app", ".bundle", ".xcassets"}

// IsBundleDir reports whether name is a bundle directory's name.
func IsBundleDir(name string) bool {
	return slices.Contains(bundleSuffixes, strings.ToLower(filepath.Ext(name)))
}

// RelPath is reference as a slash-separated path inside dir: an absolute path
// under dir, or a path relative to it, once cleaned.
func RelPath(dir, reference string) (string, bool) {
	if dir == "" {
		return "", false
	}
	rel := filepath.Clean(reference)
	if filepath.IsAbs(rel) {
		var err error
		if rel, err = filepath.Rel(filepath.Clean(dir), rel); err != nil {
			return "", false
		}
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// Errors from Registrable.
var (
	// ErrOutsideDir reports a reference outside the artifact directory.
	ErrOutsideDir = errors.New("not in the session's artifact directory")
	// ErrNotFound reports a reference to nothing.
	ErrNotFound = errors.New("no such file in the session's artifact directory")
	// ErrNotRegistrable reports something that cannot be an artifact: a
	// directory that is not a bundle, a symlink, or a path List leaves out.
	ErrNotRegistrable = errors.New("only a file or a bundle directory can be registered")
)

// Registrable validates reference and observes its file identity, for a regular
// file or bundle directory that List can reach.
func Registrable(dir, reference string) (domain.SessionArtifactRegistration, error) {
	rel, ok := RelPath(dir, reference)
	if !ok {
		return domain.SessionArtifactRegistration{}, ErrOutsideDir
	}
	if rel == "." || Skips(rel) {
		return domain.SessionArtifactRegistration{}, ErrNotRegistrable
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.SessionArtifactRegistration{}, ErrNotFound
	}
	if err != nil {
		return domain.SessionArtifactRegistration{}, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(filepath.FromSlash(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return domain.SessionArtifactRegistration{}, ErrNotFound
	}
	if err != nil {
		return domain.SessionArtifactRegistration{}, ErrNotRegistrable //nolint:nilerr // os.Root refuses a path that leaves dir
	}
	if info.Mode().IsRegular() || (info.IsDir() && IsBundleDir(info.Name())) {
		return domain.SessionArtifactRegistration{Path: rel, File: fullIdentity(filepath.Join(dir, filepath.FromSlash(rel)), info)}, nil
	}
	return domain.SessionArtifactRegistration{}, ErrNotRegistrable
}

// ListRegistered is List with Registered set on each file that a path in
// registered names, or that sits inside a bundle directory it names. List stops
// at MaxFiles in lexical order, so assets/ can use up the budget before
// brand.html: a registered path the walk did not reach is added here, which is
// why the result can pass MaxFiles. A path that names nothing is ignored.
func ListRegistered(ctx context.Context, dir string, registered []string) ([]domain.SessionArtifactFile, error) {
	return listRegistered(ctx, dir, registered, nil)
}

func listRegistered(ctx context.Context, dir string, registered []string, quick map[string]domain.FileIdentity) ([]domain.SessionArtifactFile, error) {
	files, err := list(ctx, dir, quick)
	if err != nil || len(registered) == 0 {
		return files, err
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		seen[file.Path] = true
	}
	for _, path := range registered {
		missing, err := missedFiles(ctx, dir, path, seen, quick)
		if err != nil {
			return nil, err
		}
		files = append(files, missing...)
	}
	names := make(map[string]bool, len(registered))
	for _, path := range registered {
		names[path] = true
	}
	for i := range files {
		files[i].Registered = namedBy(files[i].Path, names)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

// namedBy reports whether path, or a directory above it, is in names.
func namedBy(path string, names map[string]bool) bool {
	for {
		if names[path] {
			return true
		}
		cut := strings.LastIndexByte(path, '/')
		if cut < 0 {
			return false
		}
		path = path[:cut]
	}
}

// missedFiles returns the files at the registered path rel that are not in seen.
func missedFiles(ctx context.Context, dir, rel string, seen map[string]bool, quick map[string]domain.FileIdentity) ([]domain.SessionArtifactFile, error) {
	if seen[rel] {
		return nil, nil
	}
	registration, err := Registrable(dir, rel)
	if err != nil {
		return nil, nil //nolint:nilerr // a path that names nothing listable is ignored, not fatal
	}
	path := registration.Path
	full := filepath.Join(dir, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, nil //nolint:nilerr // removed since the check
	}
	if !info.IsDir() {
		if quick != nil {
			quick[path] = quickIdentity(info)
		}
		return []domain.SessionArtifactFile{artifactFile(path, info)}, nil
	}
	var inner map[string]domain.FileIdentity
	if quick != nil {
		inner = make(map[string]domain.FileIdentity)
	}
	inside, err := list(ctx, full, inner)
	if err != nil {
		return nil, err
	}
	var missing []domain.SessionArtifactFile
	for _, file := range inside {
		id, known := inner[file.Path]
		file.Path = path + "/" + file.Path
		file.Kind = inferKind(file.Path)
		if !seen[file.Path] {
			missing = append(missing, file)
			if known {
				quick[file.Path] = id
			}
		}
	}
	return missing, nil
}

func artifactFile(rel string, info fs.FileInfo) domain.SessionArtifactFile {
	return domain.SessionArtifactFile{
		Path:      rel,
		Name:      info.Name(),
		Kind:      inferKind(rel),
		Size:      info.Size(),
		UpdatedAt: info.ModTime().UTC(),
	}
}

func inferKind(relPath string) domain.SessionArtifactKind {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".html", ".htm":
		return domain.SessionArtifactHTML
	case ".md", ".markdown":
		return domain.SessionArtifactMarkdown
	}
	return domain.SessionArtifactGeneric
}

// DeriveOutputType classifies a session's durable output from counts alone.
// PR and artifact are independent facts that combine into PRAndArtifact when
// both are present. Once a PR row exists for a session it is never deleted
// (merge/close does not remove it), so prCount only grows; combined with
// artifact files only ever being added, never removed from this
// classification's perspective, the result never reverts (none -> artifact
// and/or pr -> pr_artifact).
func DeriveOutputType(prCount, artifactFileCount int) domain.SessionOutputType {
	hasPR := prCount > 0
	hasArtifact := artifactFileCount > 0
	switch {
	case hasPR && hasArtifact:
		return domain.SessionOutputPRAndArtifact
	case hasPR:
		return domain.SessionOutputPR
	case hasArtifact:
		return domain.SessionOutputArtifact
	default:
		return domain.SessionOutputNone
	}
}
