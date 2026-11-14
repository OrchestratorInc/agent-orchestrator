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

func Dir(dataDir string, id domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "artifacts", string(id))
}

const VersionsDir = ".versions"

const MaxFiles = 1000

const MaxVisited = 10000

func List(ctx context.Context, dir string) ([]domain.SessionArtifactFile, error) {
	return list(ctx, dir, nil)
}

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

func ignored(name string) bool {
	return strings.HasPrefix(name, "._") || ignoredNames[strings.ToLower(name)]
}

func Skips(rel string) bool {
	parts := strings.Split(rel, "/")
	return parts[0] == VersionsDir || slices.ContainsFunc(parts, ignored)
}

var bundleSuffixes = []string{".icon", ".app", ".bundle", ".xcassets"}

func IsBundleDir(name string) bool {
	return slices.Contains(bundleSuffixes, strings.ToLower(filepath.Ext(name)))
}

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

var (
	ErrOutsideDir     = errors.New("not in the session's artifact directory")
	ErrNotFound       = errors.New("no such file in the session's artifact directory")
	ErrNotRegistrable = errors.New("only a file or a bundle directory can be registered")
)

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
