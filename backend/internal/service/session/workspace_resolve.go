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

type WorkspacePathKind string

const (
	WorkspacePathKindWorkspace WorkspacePathKind = "workspace"
	WorkspacePathKindArtifact  WorkspacePathKind = "artifact"
	WorkspacePathKindOutside   WorkspacePathKind = "outside"
)

type WorkspacePathResolution struct {
	SessionID domain.SessionID
	Kind      WorkspacePathKind
	Path      string
}

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
	return home + strings.TrimPrefix(raw, "~")
}

var foreignAbsolutePath = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\)`)

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
