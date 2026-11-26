package scratch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Options configures the scratch workspace adapter.
type Options struct {
	ManagedRoot string
}

// Workspace creates per-session plain directories under AO's managed root.
type Workspace struct {
	managedRoot string
}

var _ ports.Workspace = (*Workspace)(nil)
var _ ports.WorkspaceObserver = (*Workspace)(nil)
var _ ports.WorkspaceReclaimer = (*Workspace)(nil)

// New validates ManagedRoot and returns a scratch workspace adapter.
func New(opts Options) (*Workspace, error) {
	if strings.TrimSpace(opts.ManagedRoot) == "" {
		return nil, errors.New("scratch workspace: ManagedRoot is required")
	}
	root, err := physicalAbs(opts.ManagedRoot)
	if err != nil {
		return nil, fmt.Errorf("scratch workspace: managed root: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("scratch workspace: create managed root: %w", err)
	}
	return &Workspace{managedRoot: filepath.Clean(root)}, nil
}

// Create materializes a branchless scratch directory for one session.
func (w *Workspace) Create(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	path, err := w.managedPath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if exists, err := pathExistsNonEmpty(path); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if exists {
		return ports.WorkspaceInfo{}, fmt.Errorf("scratch workspace: path %q already contains files: %w", path, ports.ErrWorkspaceDirty)
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return ports.WorkspaceInfo{}, fmt.Errorf("scratch workspace: create %q: %w", path, err)
	}
	return ports.WorkspaceInfo{Path: path, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}

// Restore ensures the preserved scratch directory exists and returns it.
func (w *Workspace) Restore(_ context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	path, err := w.restorePath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return ports.WorkspaceInfo{}, fmt.Errorf("scratch workspace: restore %q: %w", path, err)
	}
	return ports.WorkspaceInfo{Path: path, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, nil
}

// Destroy removes a scratch directory that holds nothing but AO's own control
// files (see scanScratchContents). Anything else is treated as dirty user work
// and preserved.
func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	_, err := w.destroy(ctx, info)
	return err
}

// DestroyReclaim is Destroy plus the reclaim outcome. os.Remove is tolerated on
// a missing path, so a scratch directory that was already gone tears down
// without error while releasing nothing; callers counting reclaimed workspaces
// need that separated from a real removal.
func (w *Workspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	return w.destroy(ctx, info)
}

func (w *Workspace) destroy(_ context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	// Only a definite absence counts as already-absent; any other stat failure
	// stays on the removed path so an unknown is never reported as "nothing to
	// do".
	reclaim := ports.WorkspaceReclaimRemoved
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		reclaim = ports.WorkspaceReclaimAlreadyAbsent
	}
	contents, err := scanScratchContents(path)
	if err != nil {
		return reclaim, err
	}
	if contents.userWork {
		return reclaim, fmt.Errorf("scratch workspace: preserve non-empty %q: %w", path, ports.ErrWorkspaceDirty)
	}
	if err := contents.remove(path); err != nil {
		return reclaim, err
	}
	return reclaim, nil
}

// scratchContents is what a scratch directory holds, split the way git splits
// a project worktree: AO's own control files, which never block teardown, and
// anything else, which is user work.
type scratchContents struct {
	userWork bool
	// managed are AO-owned files and directories, removed before dirs.
	managed []string
	// dirs are the directories under the root, parents before children.
	dirs []string
}

// scanScratchContents classifies everything under root. Agent adapters write
// their hook config beside a self-ignoring .gitignore carrying AO's sentinel,
// so a project worktree's git status never sees it. Scratch has no git index,
// so the names such a .gitignore lists, and AO's Copilot profiles, are read
// directly, as the session Files surfaces already do. Everything else, a
// symlink or a folder the user ran git init in included, is user work.
func scanScratchContents(root string) (scratchContents, error) {
	var contents scratchContents
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return contents, nil
	}
	managedByDir := map[string]managedNames{}
	err := filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fullPath == root {
			return nil
		}
		dir := filepath.Dir(fullPath)
		managed, ok := managedByDir[dir]
		if !ok {
			managed = aoManagedNamesIn(dir)
			managedByDir[dir] = managed
		}
		if managed.match(entry.Name()) {
			contents.managed = append(contents.managed, fullPath)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			contents.dirs = append(contents.dirs, fullPath)
			return nil
		}
		if aoManagedCopilotProfile(root, fullPath, entry) {
			contents.managed = append(contents.managed, fullPath)
			return nil
		}
		contents.userWork = true
		return filepath.SkipAll
	})
	if err != nil {
		return scratchContents{}, fmt.Errorf("scratch workspace: inspect %q: %w", root, err)
	}
	return contents, nil
}

// remove deletes AO's files, then each directory only if it is empty. A file
// that appeared after the scan leaves its directory non-empty, so it is kept
// and reported as dirty rather than deleted.
func (c scratchContents) remove(root string) error {
	for _, managed := range c.managed {
		if err := os.RemoveAll(managed); err != nil {
			return fmt.Errorf("scratch workspace: remove AO file %q: %w", managed, err)
		}
	}
	// Children before parents, the root last.
	dirs := append([]string{root}, c.dirs...)
	for i := len(dirs) - 1; i >= 0; i-- {
		err := os.Remove(dirs[i])
		if err == nil || errors.Is(err, os.ErrNotExist) {
			continue
		}
		if nonEmpty, _ := pathExistsNonEmpty(dirs[i]); nonEmpty {
			return fmt.Errorf("scratch workspace: preserve non-empty %q: %w", dirs[i], ports.ErrWorkspaceDirty)
		}
		return fmt.Errorf("scratch workspace: remove empty %q: %w", dirs[i], err)
	}
	return nil
}

// managedNames are the patterns an AO-managed .gitignore lists, each naming
// one entry of its directory: a literal name, or a glob such as
// "ao-standing-instructions-*.md".
type managedNames []string

func (m managedNames) match(name string) bool {
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

// aoManagedNamesIn returns the names an AO-managed .gitignore in dir lists.
// Adapters anchor each pattern to that directory. A listed directory is AO's
// whole, as git treats it in a project worktree. A missing, foreign or
// unreadable marker claims nothing, so its neighbours count as user work.
func aoManagedNamesIn(dir string) managedNames {
	var managed managedNames
	content, ok := readSmallRegularFile(filepath.Join(dir, ".gitignore"))
	if !ok || !strings.Contains(content, hookutil.GitignoreSentinel) {
		return managed
	}
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

// aoManagedCopilotProfile reports AO's Copilot agent profile, which a project
// worktree hides through .git/info/exclude and which carries its own marker.
func aoManagedCopilotProfile(root, fullPath string, entry fs.DirEntry) bool {
	rel, err := filepath.Rel(root, filepath.Dir(fullPath))
	if err != nil || filepath.ToSlash(rel) != ".github/agents" {
		return false
	}
	name := entry.Name()
	if !strings.HasPrefix(name, "ao-") || !strings.HasSuffix(name, ".agent.md") {
		return false
	}
	content, ok := readSmallRegularFile(fullPath)
	return ok && strings.Contains(content, hookutil.CopilotAgentProfileSentinel)
}

// readSmallRegularFile reads up to 64 KiB of a regular file, not following a
// symlink, so a marker can only be trusted from a file in the workspace.
func readSmallRegularFile(file string) (string, bool) {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	handle, err := os.Open(file) //nolint:gosec // path is under the validated managed root
	if err != nil {
		return "", false
	}
	defer func() { _ = handle.Close() }()
	data, err := io.ReadAll(io.LimitReader(handle, 64*1024))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ForceDestroy intentionally follows Destroy semantics for scratch. Normal AO
// paths must never force-delete non-empty scratch work.
func (w *Workspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	return w.Destroy(ctx, info)
}

// StashUncommitted is a no-op for scratch because there is no git object store.
func (w *Workspace) StashUncommitted(context.Context, ports.WorkspaceInfo) (string, error) {
	return "", nil
}

// ApplyPreserved is a no-op unless a caller incorrectly supplies a git ref.
func (w *Workspace) ApplyPreserved(_ context.Context, _ ports.WorkspaceInfo, ref string) error {
	if strings.TrimSpace(ref) != "" {
		return errors.New("scratch workspace: preserved refs are not supported")
	}
	return nil
}

// AddExclude is a no-op for scratch because scratch workspaces are plain
// directories with no git status or commit surface.
func (w *Workspace) AddExclude(context.Context, ports.WorkspaceInfo, ...string) error {
	return nil
}

// ObserveWorkspace validates the managed path and returns a path-only
// observation. Scratch projects intentionally have no fabricated Git facts.
func (w *Workspace) ObserveWorkspace(_ context.Context, info ports.WorkspaceInfo) (ports.WorkspaceObservation, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return ports.WorkspaceObservation{}, err
	}
	return ports.WorkspaceObservation{Path: path}, nil
}

func (w *Workspace) managedPath(cfg ports.WorkspaceConfig) (string, error) {
	if cfg.SessionID == "" {
		return "", errors.New("scratch workspace: session id is required")
	}
	if err := validatePathComponent("session id", string(cfg.SessionID)); err != nil {
		return "", err
	}
	if cfg.ProjectID == "" {
		return w.validateManagedPath(filepath.Join(w.managedRoot, "standalone", "sessions", string(cfg.SessionID)))
	}
	if err := validatePathComponent("project id", string(cfg.ProjectID)); err != nil {
		return "", err
	}
	roleDir := "workers"
	if cfg.Kind == domain.KindOrchestrator {
		roleDir = "orchestrators"
	}
	return w.validateManagedPath(filepath.Join(w.managedRoot, string(cfg.ProjectID), roleDir, string(cfg.SessionID)))
}

func (w *Workspace) restorePath(cfg ports.WorkspaceConfig) (string, error) {
	if strings.TrimSpace(cfg.Path) != "" {
		return w.validateManagedPath(filepath.Clean(cfg.Path))
	}
	return w.managedPath(cfg)
}

func validatePathComponent(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("scratch workspace: %s is required", name)
	}
	if strings.ContainsAny(value, `/\`) || value == "." || value == ".." {
		return fmt.Errorf("scratch workspace: %s %q must not contain path separators or traversal components", name, value)
	}
	return nil
}

func (w *Workspace) validateManagedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("scratch workspace: empty path: %w", ports.ErrWorkspaceUnsafePath)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("scratch workspace: %q is not absolute: %w", path, ports.ErrWorkspaceUnsafePath)
	}
	clean := filepath.Clean(path)
	physical, err := physicalAbs(clean)
	if err != nil {
		return "", fmt.Errorf("scratch workspace: resolve path %q: %w", path, err)
	}
	inside, err := pathWithin(w.managedRoot, physical)
	if err != nil {
		return "", err
	}
	if !inside || samePath(physical, w.managedRoot) {
		return "", fmt.Errorf("scratch workspace: path %q is outside managed root %q: %w", physical, w.managedRoot, ports.ErrWorkspaceUnsafePath)
	}
	return physical, nil
}

func pathExistsNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries) > 0, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("scratch workspace: inspect %q: %w", path, err)
}

func physicalAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	for parent != "." && parent != string(os.PathSeparator) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, base), nil
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = filepath.Dir(parent)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base), nil
	}
	return abs, nil
}

func pathWithin(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, fmt.Errorf("scratch workspace: compare paths: %w", err)
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))), nil
}

func samePath(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	return a == b
}
