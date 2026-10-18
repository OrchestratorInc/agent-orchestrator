package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

// WorkspaceLocation returns the live workspace directory for a session. It is
// deliberately narrower than the normal session read model: Electron main is
// the only consumer, and the renderer must never receive this absolute path.
func (s *Service) WorkspaceLocation(ctx context.Context, id domain.SessionID) (string, error) {
	record, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get session %s workspace: %w", id, err)
	}
	if !ok {
		return "", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	// Orchestrators run directly in the project checkout and are never assigned
	// a per-session worktree, so Metadata.WorkspacePath is empty by design.
	// Their workspace location is the project's own path — without this branch
	// the desktop editor-handoff probe 404s and the topbar permanently flags
	// every orchestrator session as "Session workspace is not available".
	// Workers and legacy records with no kind keep the strict Metadata-only
	// contract below: a worker whose worktree vanished must surface as
	// unavailable, not silently redirect the editor to the project checkout.
	if record.Kind == domain.KindOrchestrator {
		project, ok, err := s.store.GetProject(ctx, string(record.ProjectID))
		if err != nil {
			return "", fmt.Errorf("get project %s workspace: %w", record.ProjectID, err)
		}
		if !ok {
			return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available")
		}
		return workspaceDir(id, project.Path)
	}

	return workspaceDir(id, record.Metadata.WorkspacePath)
}

// workspaceDir validates workspacePath as an existing absolute directory and
// returns it cleaned. Definite absence is "not available"; structural
// unreadability is a 409; anything else bubbles up as a wrapped error.
func workspaceDir(id domain.SessionID, workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" || !filepath.IsAbs(workspacePath) {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available")
	}
	// Only a definite absence is "not found"; any other stat failure may be transient
	// and must not tell callers the worktree is gone.
	info, err := os.Stat(workspacePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available")
	}
	// These won't clear on their own, so a 500 would only make the caller retry forever.
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return "", apierr.Conflict("SESSION_WORKSPACE_UNREADABLE", "AO can't read this session's folder. Check its permissions.", nil)
	}
	if err != nil {
		return "", fmt.Errorf("stat session %s workspace: %w", id, err)
	}
	return filepath.Clean(workspacePath), nil
}

// ArtifactLocation returns a session's artifact directory, for Electron main
// only. A row stored without one uses the default layout under the data dir.
func (s *Service) ArtifactLocation(ctx context.Context, id domain.SessionID) (string, error) {
	record, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get session %s artifact dir: %w", id, err)
	}
	if !ok {
		return "", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	dir := strings.TrimSpace(record.Metadata.ArtifactDir)
	if dir == "" {
		dir = sessionartifacts.Dir(s.dataDir, record.ID)
	}
	notFound := apierr.NotFound("SESSION_ARTIFACTS_NOT_FOUND", "This session has no artifacts folder yet.")
	if dir == "" || !filepath.IsAbs(dir) {
		return "", notFound
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return "", notFound
	}
	if err != nil {
		return "", fmt.Errorf("stat session %s artifact dir: %w", id, err)
	}
	return filepath.Clean(dir), nil
}
