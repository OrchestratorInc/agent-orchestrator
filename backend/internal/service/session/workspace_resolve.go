package session

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// WorkspacePathKind says which session-owned root a path belongs to.
type WorkspacePathKind string

const (
	// WorkspacePathKindWorkspace is a path inside the session worktree.
	WorkspacePathKindWorkspace WorkspacePathKind = "workspace"
	// WorkspacePathKindArtifact is a path inside the session artifact directory.
	WorkspacePathKindArtifact WorkspacePathKind = "artifact"
	// WorkspacePathKindOutside is a path in neither root.
	WorkspacePathKindOutside WorkspacePathKind = "outside"
)

// WorkspacePathResolution is a path reduced to the root that owns it. Path is
// relative to that root and empty for WorkspacePathKindOutside, so the caller
// never learns a local absolute path.
type WorkspacePathResolution struct {
	SessionID domain.SessionID
	Kind      WorkspacePathKind
	Path      string
}

// ResolveWorkspacePath classifies a path an agent wrote into chat. Agents link
// files by absolute path, but the renderer is never told where a session's
// worktree lives, so it asks here instead of guessing from the file catalog.
func (s *Service) ResolveWorkspacePath(ctx context.Context, id domain.SessionID, rawPath string) (WorkspacePathResolution, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return WorkspacePathResolution{}, err
	}
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return WorkspacePathResolution{}, apierr.Invalid("INVALID_WORKSPACE_PATH", "path is required", nil)
	}
	target := expandHomePath(trimmed)
	if !filepath.IsAbs(target) {
		// A rooted path that is not absolute here (a Unix path on a Windows
		// daemon, a Windows drive or UNC path on a Unix one, or a drive-relative
		// path) names somewhere this session does not own; it is not a
		// malformed relative path.
		if path.IsAbs(filepath.ToSlash(target)) || filepath.VolumeName(target) != "" || foreignAbsolutePath.MatchString(target) {
			return WorkspacePathResolution{SessionID: id, Kind: WorkspacePathKindOutside}, nil
		}
		rel, err := cleanWorkspaceRelativePath(trimmed)
		if err != nil {
			return WorkspacePathResolution{}, err
		}
		return WorkspacePathResolution{SessionID: id, Kind: WorkspacePathKindWorkspace, Path: rel}, nil
	}
	if rel, ok := relativeWithinRoot(rec.Metadata.WorkspacePath, target); ok {
		return WorkspacePathResolution{SessionID: id, Kind: WorkspacePathKindWorkspace, Path: rel}, nil
	}
	artifactDir := rec.Metadata.ArtifactDir
	if artifactDir == "" {
		artifactDir = sessionartifacts.Dir(s.dataDir, rec.ID)
	}
	if rel, ok := relativeWithinRoot(artifactDir, target); ok {
		return WorkspacePathResolution{SessionID: id, Kind: WorkspacePathKindArtifact, Path: rel}, nil
	}
	return WorkspacePathResolution{SessionID: id, Kind: WorkspacePathKindOutside}, nil
}

func expandHomePath(raw string) string {
	if raw != "~" && !strings.HasPrefix(raw, "~/") {
		return raw
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return raw
	}
	// Not filepath.Join: it would collapse a ".." lexically, before any
	// symlink ahead of it has been followed.
	return home + strings.TrimPrefix(raw, "~")
}

// foreignAbsolutePath matches Windows drive (C:\x, C:/x) and UNC (\\host\share)
// spellings, which filepath does not treat as absolute on other platforms.
var foreignAbsolutePath = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\)`)

// relativeWithinRoot reports target relative to root when target is strictly
// inside it. Both are resolved the way the filesystem reads them, so a link
// inside the root that points out of it is not mistaken for a file the session
// owns, and the returned path names the file the target actually reaches.
func relativeWithinRoot(root, target string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return "", false
	}
	resolvedRoot, resolvedTarget := resolveInFilesystemOrder(root), resolveInFilesystemOrder(target)
	if !pathWithin(resolvedRoot, resolvedTarget) {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// resolveInFilesystemOrder resolves an absolute path one component at a time,
// following each symlink before applying the ".." after it. Cleaning first
// would be wrong: with link -> /elsewhere/dir, "root/link/../file" names
// /elsewhere/file, not root/file. Once a component does not exist the rest is
// joined as written, since a missing directory holds no links to follow.
func resolveInFilesystemOrder(absolute string) string {
	volume := filepath.VolumeName(absolute)
	resolved := volume + string(filepath.Separator)
	parts := strings.FieldsFunc(absolute[len(volume):], func(r rune) bool { return r == '/' || r == filepath.Separator })
	for i, part := range parts {
		switch part {
		case ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next, err := resolvedFilesystemPath(filepath.Join(resolved, part))
		if err != nil {
			return filepath.Join(append([]string{resolved}, parts[i:]...)...)
		}
		resolved = next
	}
	return resolved
}
