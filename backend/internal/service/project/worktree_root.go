package project

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// WorktreeRoot resolution and safety rules.
//
// A project's worktree root decides where every NEW session worktree of that
// project lands (<root>/<project-id>/<session-id>). The rules live here, in the
// project service, because they depend on the project registry: a root is only
// safe relative to the project's own repository and every other project's root.
// The workspace adapter still owns per-session materialisation and re-checks the
// snapshot it is handed (see gitworktree.Workspace).

// EffectiveWorktreeRoot is the root a project actually uses: its configured
// WorktreeRoot, or the installation default when the config leaves it empty
// (projects registered before the field existed).
func EffectiveWorktreeRoot(cfg domain.ProjectConfig, dataDir string) string {
	if root := strings.TrimSpace(cfg.WorktreeRoot); root != "" {
		return filepath.Clean(root)
	}
	return domain.DefaultWorktreeRoot(dataDir)
}

// applyDefaultWorktreeRoot fills a registration config's WorktreeRoot with the
// installation default. New projects persist the resolved value so a later
// AO_DATA_DIR change does not silently move their worktrees, while a project
// that already names a root keeps it untouched.
func applyDefaultWorktreeRoot(cfg *domain.ProjectConfig, dataDir string) {
	if cfg == nil || strings.TrimSpace(cfg.WorktreeRoot) != "" {
		return
	}
	cfg.WorktreeRoot = domain.DefaultWorktreeRoot(dataDir)
}

// validateWorktreeRootSelection applies the filesystem-facing half of the
// worktree-root contract to an EXPLICIT root being saved: the directory is
// created when missing, must be writable, and must not sit inside the project's
// own repository, equal another project's root, or nest with one. It is a no-op
// for an empty root, which means the unchanged installation default.
//
// Roots are recorded and compared in physical (symlink-resolved) form so two
// spellings of one directory cannot both pass the equality check.
func (m *Service) validateWorktreeRootSelection(ctx context.Context, projectID domain.ProjectID, projectPath, root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", nil
	}
	if err := domain.ValidateWorktreeRoot(trimmed); err != nil {
		return "", apierr.Invalid("INVALID_WORKTREE_ROOT", err.Error(), map[string]any{"worktreeRoot": trimmed})
	}
	clean := filepath.Clean(trimmed)
	physical, created, err := m.prepareWorktreeRootDirectory(clean)
	if err != nil {
		return "", err
	}
	// Any later rule can still reject this root. A folder this call created must
	// not be left behind by that rejection; a folder that already existed is
	// never removed, so a failed save cannot destroy a user's directory.
	reject := func(err error) (string, error) {
		if created {
			removeCreatedWorktreeRoot(clean)
		}
		return "", err
	}
	if err := rejectWorktreeRootInsideRepo(projectID, projectPath, physical); err != nil {
		return reject(err)
	}
	if err := m.rejectWorktreeRootConflicts(ctx, projectID, physical); err != nil {
		return reject(err)
	}
	return physical, nil
}

// removeCreatedWorktreeRoot removes a directory this call created and nothing
// else. It re-checks that the entry is still the empty plain directory Mkdir made:
// a junction or symlink that appeared there in the meantime (created=true applies
// only to a path that did not exist a moment ago, but the check costs nothing)
// would make os.Remove delete the link target's contents on some platforms, so a
// reparse point is never removed here.
func removeCreatedWorktreeRoot(path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return
	}
	_ = os.Remove(path)
}

// prepareWorktreeRootDirectory makes sure root exists as a writable directory,
// creating it ONLY beneath an existing parent. It returns root's physical path.
func (m *Service) prepareWorktreeRootDirectory(root string) (physicalPath string, created bool, err error) {
	info, statErr := os.Stat(root)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return "", false, apierr.Invalid("WORKTREE_ROOT_UNAVAILABLE", "The worktree root could not be inspected.", map[string]any{"worktreeRoot": root})
	}
	if statErr == nil && !info.IsDir() {
		return "", false, apierr.Invalid("WORKTREE_ROOT_NOT_DIRECTORY", "The worktree root must be a folder.", map[string]any{"worktreeRoot": root})
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		// The parent only has to EXIST; its resolved identity is irrelevant here.
		// filepath.EvalSymlinks is the wrong probe: on Windows it fails for a path
		// that traverses a directory junction even though the folder is right
		// there, which turned a valid new child root into WORKTREE_ROOT_PARENT_MISSING.
		if parentInfo, parentErr := os.Stat(filepath.Dir(root)); parentErr != nil || !parentInfo.IsDir() {
			return "", false, apierr.Invalid("WORKTREE_ROOT_PARENT_MISSING",
				"The worktree root's parent folder does not exist. Create it first, or choose another folder.",
				map[string]any{"worktreeRoot": root, "parent": filepath.Dir(root)})
		}
		if mkErr := os.Mkdir(root, 0o750); mkErr != nil {
			if errors.Is(mkErr, fs.ErrPermission) {
				return "", false, apierr.Invalid("WORKTREE_ROOT_NOT_WRITABLE", "AO is not allowed to create the worktree root folder.", map[string]any{"worktreeRoot": root})
			}
			return "", false, apierr.Invalid("WORKTREE_ROOT_UNAVAILABLE", "AO could not create the worktree root folder.", map[string]any{"worktreeRoot": root})
		}
		created = true
	}
	// Every root is returned in a form that lets two spellings of one directory
	// compare equal, so a junction or symlinked path cannot pass the cross-project
	// conflict check alongside its target.
	physical, evalErr := physicalWorktreeRoot(root)
	if evalErr != nil {
		return "", created, apierr.Invalid("WORKTREE_ROOT_UNAVAILABLE", "The worktree root could not be resolved.", map[string]any{"worktreeRoot": root})
	}
	if usableErr := m.requireWorktreeRootUsable(physical); usableErr != nil {
		return "", created, usableErr
	}
	return physical, created, nil
}

// physicalWorktreeRoot resolves a root to the path that identifies its directory,
// so two spellings of one directory (a symlink or a Windows junction and its
// target) compare equal and cannot both pass the cross-project conflict check.
//
// filepath.EvalSymlinks is not enough on Windows: it resolves symlinks but leaves a
// directory JUNCTION unresolved, so the junction path and its target would compare
// different even though they address one directory. resolveDirectoryIdentity closes
// that gap per platform. A path that cannot be resolved is reported as an error
// rather than returned unchanged, because an unresolved spelling is exactly what
// would let a duplicate root through.
func physicalWorktreeRoot(root string) (string, error) {
	clean := filepath.Clean(root)
	resolved, err := resolveDirectoryIdentity(clean)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// requireWorktreeRootUsable reports whether AO can write beneath root. An
// existing non-empty directory is refused unless it already holds AO-managed
// worktrees (directories laid out as AO's own root is), so pointing a project at
// a folder full of unrelated files fails instead of scattering worktrees through
// it. The AO-managed shape is accepted because it covers the installation
// default root, which every project shares.
func (m *Service) requireWorktreeRootUsable(root string) error {
	if err := m.requireWritableDirectory(root); err != nil {
		return err
	}
	// The installation default root holds one directory per project and a trash
	// folder; it is AO's own by construction, so it never needs the shape check.
	if defaultRoot := domain.DefaultWorktreeRoot(m.dataDir); defaultRoot != "" {
		if physicalDefault, err := physicalPathIfExists(defaultRoot); err == nil && samePath(physicalDefault, root) {
			return nil
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return apierr.Invalid("WORKTREE_ROOT_UNAVAILABLE", "The worktree root could not be read.", map[string]any{"worktreeRoot": root})
	}
	for _, entry := range entries {
		// The trash folder the workspace adapter keeps beside session worktrees.
		if entry.Name() == ".discarded" {
			continue
		}
		if !entry.IsDir() {
			return apierr.Invalid("WORKTREE_ROOT_NOT_EMPTY",
				"The worktree root must be empty or already hold AO-managed worktrees.",
				map[string]any{"worktreeRoot": root, "entry": entry.Name()})
		}
	}
	return nil
}

// requireWritableDirectory proves root accepts new files for AO by creating and
// removing a probe file. Mode bits alone are not evidence: ACLs, read-only
// mounts, and container bind mounts all deny writes on directories that look
// writable.
func (m *Service) requireWritableDirectory(root string) error {
	probe, err := os.CreateTemp(root, ".ao-write-check-")
	if err != nil {
		return apierr.Invalid("WORKTREE_ROOT_NOT_WRITABLE", "AO cannot write to the worktree root folder.", map[string]any{"worktreeRoot": root})
	}
	name := probe.Name()
	if _, err := io.Copy(io.Discard, probe); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return apierr.Invalid("WORKTREE_ROOT_NOT_WRITABLE", "AO cannot write to the worktree root folder.", map[string]any{"worktreeRoot": root})
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return apierr.Invalid("WORKTREE_ROOT_NOT_WRITABLE", "AO cannot write to the worktree root folder.", map[string]any{"worktreeRoot": root})
	}
	if err := os.Remove(name); err != nil {
		return apierr.Invalid("WORKTREE_ROOT_NOT_WRITABLE", "AO cannot write to the worktree root folder.", map[string]any{"worktreeRoot": root})
	}
	return nil
}

// rejectWorktreeRootInsideRepo refuses a root that lies inside the project's own
// repository: git cannot check out a worktree of a repository into that same
// repository without the checkout polluting its own working tree.
func rejectWorktreeRootInsideRepo(projectID domain.ProjectID, projectPath, root string) error {
	if strings.TrimSpace(projectPath) == "" {
		return nil
	}
	repoPhysical, err := physicalPathIfExists(projectPath)
	if err != nil {
		// The repository cannot be resolved right now (for example a detached
		// volume). Spawn re-checks this, and refusing a save because the repo is
		// temporarily unreadable would block unrelated settings edits.
		return nil
	}
	inside, err := pathWithin(repoPhysical, root)
	if err != nil {
		return apierr.Invalid("INVALID_WORKTREE_ROOT", "The worktree root could not be compared with the project repository.", map[string]any{"worktreeRoot": root})
	}
	if inside {
		return apierr.Invalid("WORKTREE_ROOT_INSIDE_REPO",
			"The worktree root cannot be inside the project repository.",
			map[string]any{"worktreeRoot": root, "projectId": string(projectID), "path": projectPath})
	}
	return nil
}

// rejectWorktreeRootConflicts refuses a root that equals another project's root,
// or nests with it in either direction: worktrees from two projects would
// otherwise become indistinguishable, and one project's cleanup could reach the
// other's checkouts.
func (m *Service) rejectWorktreeRootConflicts(ctx context.Context, projectID domain.ProjectID, root string) error {
	// The installation default root holds one directory per project by
	// construction, so every project shares it and none of them collides with
	// another. Only a root that moves a project OFF that layout must be unique.
	if defaultRoot := domain.DefaultWorktreeRoot(m.dataDir); defaultRoot != "" {
		if physicalDefault, err := physicalPathIfExists(defaultRoot); err == nil && samePath(physicalDefault, root) {
			return nil
		}
	}
	rows, err := m.store.ListProjects(ctx)
	if err != nil {
		return apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	for _, row := range rows {
		if domain.ProjectID(row.ID) == projectID || row.Kind.WithDefault() == domain.ProjectKindScratch {
			continue
		}
		other := EffectiveWorktreeRoot(row.Config, m.dataDir)
		if other == "" {
			continue
		}
		otherPhysical, err := physicalPathIfExists(other)
		if err != nil {
			// A root that does not exist yet can only collide by path spelling,
			// which Clean plus the equality check below still catches.
			otherPhysical = filepath.Clean(other)
		}
		nested, err := worktreeRootsNest(root, otherPhysical)
		if err != nil {
			return apierr.Invalid("INVALID_WORKTREE_ROOT", "The worktree root could not be compared with other projects.", map[string]any{"worktreeRoot": root})
		}
		if nested {
			return apierr.Conflict("WORKTREE_ROOT_CONFLICT",
				"That folder conflicts with another project's worktree root. Choose a folder that is neither the same as nor inside another project's root.",
				map[string]any{"worktreeRoot": root, "conflictingProjectId": row.ID, "conflictingRoot": other})
		}
	}
	return nil
}

// worktreeRootsNest reports whether two roots are equal or one contains the
// other. Two volumes never nest, so a Windows D:\ root and a C:\ root stay
// independent.
func worktreeRootsNest(a, b string) (bool, error) {
	insideAB, err := pathWithin(a, b)
	if err != nil {
		return false, err
	}
	if insideAB {
		return true, nil
	}
	return pathWithin(b, a)
}

// pathWithin reports whether target is parent or lies beneath it, comparing
// clean absolute paths. parent==target counts as within.
func pathWithin(parent, target string) (bool, error) {
	parentAbs, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return false, err
	}
	targetAbs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false, err
	}
	if samePath(parentAbs, targetAbs) {
		return true, nil
	}
	rel, err := filepath.Rel(parentAbs, targetAbs)
	if err != nil {
		return false, nil
	}
	if rel == "." {
		return true, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

// physicalPathIfExists resolves an existing path to the directory identity that
// comparisons use, and returns a cleaned absolute path when it does not exist yet.
//
// It MUST use the same resolution as physicalWorktreeRoot: comparing a root
// resolved through a Windows junction handle against a path resolved through
// filepath.EvalSymlinks (which leaves junctions unresolved) makes one directory
// look like two, so a duplicate or nested root could pass the conflict check.
func physicalPathIfExists(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(abs); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return abs, nil
		}
		return "", statErr
	}
	resolved, err := resolveDirectoryIdentity(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}
