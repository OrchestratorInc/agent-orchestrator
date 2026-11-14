package scratch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Options struct {
	ManagedRoot string
}

type Workspace struct {
	managedRoot string
}

var _ ports.Workspace = (*Workspace)(nil)
var _ ports.WorkspaceObserver = (*Workspace)(nil)
var _ ports.WorkspaceReclaimer = (*Workspace)(nil)

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

func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	_, err := w.destroy(ctx, info)
	return err
}

func (w *Workspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	return w.destroy(ctx, info)
}

func (w *Workspace) destroy(_ context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	reclaim := ports.WorkspaceReclaimRemoved
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		reclaim = ports.WorkspaceReclaimAlreadyAbsent
	}
	nonEmpty, err := pathExistsNonEmpty(path)
	if err != nil {
		return reclaim, err
	}
	if nonEmpty {
		return reclaim, fmt.Errorf("scratch workspace: preserve non-empty %q: %w", path, ports.ErrWorkspaceDirty)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return reclaim, fmt.Errorf("scratch workspace: remove empty %q: %w", path, err)
	}
	return reclaim, nil
}

func (w *Workspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	return w.Destroy(ctx, info)
}

func (w *Workspace) StashUncommitted(context.Context, ports.WorkspaceInfo) (string, error) {
	return "", nil
}

func (w *Workspace) ApplyPreserved(_ context.Context, _ ports.WorkspaceInfo, ref string) error {
	if strings.TrimSpace(ref) != "" {
		return errors.New("scratch workspace: preserved refs are not supported")
	}
	return nil
}

func (w *Workspace) AddExclude(context.Context, ports.WorkspaceInfo, ...string) error {
	return nil
}

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
