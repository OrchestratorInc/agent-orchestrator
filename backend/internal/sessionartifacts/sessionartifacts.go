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
	"runtime"
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
	return list(ctx, dir, nil, MaxFiles)
}

// errFound stops HasFiles' walk at the first file.
var errFound = errors.New("found")

// HasFiles reports whether a session's artifact directory holds any file List
// would return. It stops at the first one, so a sweep over many sessions does
// not walk whole trees. A missing directory has none.
//
// Unlike List it is strict, since a false answer lets the caller delete the
// directory: an entry it cannot read or stat is an error, and a walk that
// reaches MaxVisited without an answer counts as having files.
func HasFiles(ctx context.Context, dir string) (bool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	visited := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > MaxVisited {
			return errFound
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
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			return errFound
		}
		return nil
	})
	switch {
	case errors.Is(err, errFound):
		return true, nil
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		// A missing artifact directory reads as empty; a file vanishing
		// mid-walk, or a root that cannot be checked again, stays an error.
		_, statErr := os.Lstat(root)
		if errors.Is(statErr, fs.ErrNotExist) {
			return false, nil
		}
		if statErr != nil {
			return false, statErr
		}
		return false, err
	default:
		return false, err
	}
}

// list is List, stopping after maxFiles files, that also records each file's
// identity in quick, when quick is not nil.
func list(ctx context.Context, dir string, quick map[string]domain.FileIdentity, maxFiles int) ([]domain.SessionArtifactFile, error) {
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
		if len(files) >= maxFiles || visited > MaxVisited {
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
// under dir, or a path relative to it, once cleaned. An absolute path that
// reaches dir another way counts too; see relUnder. On Windows a Git Bash,
// Cygwin or WSL drive path such as /c/Users/… is read as C:/Users/….
func RelPath(dir, reference string) (string, bool) {
	if dir == "" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		reference = WindowsDrivePath(reference)
	}
	rel := filepath.Clean(reference)
	if filepath.IsAbs(rel) {
		rel = relUnder(filepath.Clean(dir), rel, caseInsensitiveOS(runtime.GOOS))
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// relUnder is target relative to dir, both clean and absolute, or ".." when
// target is not under dir. When the plain comparison fails it compares again
// with symlinks resolved in dir and in target's parent (macOS /var is
// /private/var, and ~/.ao can be a link), and with letter case ignored where
// the file system ignores it. Target's own last element is never resolved, so
// a symlink artifact is still refused as one.
func relUnder(dir, target string, foldCase bool) string {
	if rel, ok := localRel(dir, target); ok {
		return rel
	}
	realDir := resolveExisting(dir)
	realTarget := filepath.Join(resolveExisting(filepath.Dir(target)), filepath.Base(target))
	if rel, ok := localRel(realDir, realTarget); ok {
		return rel
	}
	if foldCase {
		for _, pair := range [][2]string{{dir, target}, {realDir, realTarget}} {
			if rel, ok := foldedRel(pair[0], pair[1]); ok {
				return rel
			}
		}
	}
	return ".."
}

func localRel(dir, target string) (string, bool) {
	rel, err := filepath.Rel(dir, target)
	return rel, err == nil && filepath.IsLocal(rel)
}

// resolveExisting is path with symlinks resolved in its longest existing
// ancestor, so a file not written yet still compares by its real directory.
func resolveExisting(path string) string {
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(path, rest)
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
}

// foldedRel is target relative to dir when target is under dir ignoring letter
// case, the way macOS and Windows file systems compare names by default.
func foldedRel(dir, target string) (string, bool) {
	prefix := dir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if len(target) <= len(prefix) || !strings.EqualFold(target[:len(prefix)], prefix) {
		return "", false
	}
	rel := target[len(prefix):]
	return rel, filepath.IsLocal(rel)
}

func caseInsensitiveOS(goos string) bool {
	return goos == "darwin" || goos == "windows"
}

// WindowsDrivePath turns a Git Bash (/c/…), Cygwin (/cygdrive/c/…) or WSL
// (/mnt/c/…) drive path into the C:/… path Windows uses, and returns any
// other path unchanged. Windows reads such a path as rooted on the current
// drive with no drive letter, which is never inside an artifact directory, so
// reading it as a drive path can only turn a refusal into the file meant.
func WindowsDrivePath(path string) string {
	slashed := strings.ReplaceAll(path, `\`, "/")
	for _, prefix := range []string{"/cygdrive/", "/mnt/", "/"} {
		rest, ok := strings.CutPrefix(slashed, prefix)
		if !ok || rest == "" || !isASCIILetter(rest[0]) || (len(rest) > 1 && rest[1] != '/') {
			continue
		}
		return strings.ToUpper(rest[:1]) + ":/" + strings.TrimPrefix(rest[1:], "/")
	}
	return path
}

func isASCIILetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// FromWorkingDir is reference as the agent's shell meant it from workingDir,
// the absolute folder it ran ao artifact add in. A relative reference is read
// from workingDir when that folder is inside dir and the file is there, so
// `ao artifact add index.html` works from a subfolder of the artifact
// directory. Anywhere else, or when the file is not there, it stays relative
// to dir as before.
func FromWorkingDir(dir, workingDir, reference string) string {
	if workingDir == "" || !filepath.IsAbs(workingDir) {
		return reference
	}
	native := reference
	if runtime.GOOS == "windows" {
		native = WindowsDrivePath(reference)
	}
	if native == "" || filepath.IsAbs(native) || filepath.VolumeName(native) != "" || strings.HasPrefix(filepath.ToSlash(native), "/") {
		return reference
	}
	if _, ok := RelPath(dir, workingDir); !ok {
		return reference
	}
	joined := filepath.Join(workingDir, native)
	if _, err := os.Lstat(joined); err != nil {
		return reference
	}
	return joined
}

// SplitAtSpace reports whether reference looks like dir cut off at a space:
// the start of a path that contains spaces, passed unquoted to a shell that
// split it into several arguments.
func SplitAtSpace(dir, reference string) bool {
	if runtime.GOOS == "windows" {
		reference = WindowsDrivePath(reference)
	}
	ref := filepath.ToSlash(filepath.Clean(reference))
	full := filepath.ToSlash(filepath.Clean(dir))
	return len(ref) > 1 && len(ref) < len(full) && full[len(ref)] == ' ' && strings.EqualFold(full[:len(ref)], ref)
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
	files, err := list(ctx, dir, quick, MaxFiles)
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
	inside, err := list(ctx, full, inner, MaxFiles)
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
