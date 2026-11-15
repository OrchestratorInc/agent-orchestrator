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

func (s *Service) WorkspaceLocation(ctx context.Context, id domain.SessionID) (string, error) {
	record, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get session %s workspace: %w", id, err)
	}
	if !ok {
		return "", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	workspacePath := strings.TrimSpace(record.Metadata.WorkspacePath)
	if workspacePath == "" || !filepath.IsAbs(workspacePath) {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available")
	}
	info, err := os.Stat(workspacePath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()) {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace is not available")
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return "", apierr.Conflict("SESSION_WORKSPACE_UNREADABLE", "Orchestrator can't read this session's folder. Check its permissions.", nil)
	}
	if err != nil {
		return "", fmt.Errorf("stat session %s workspace: %w", id, err)
	}
	return filepath.Clean(workspacePath), nil
}

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
