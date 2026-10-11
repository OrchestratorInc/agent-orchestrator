package attachmentstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Renders are agent-published HTML pages shown inline in chat. They share the
// session's canonical directory so RemoveSession deletes them with the session,
// but validateName rejects their "render-" prefix: a render is never projected
// into a worktree, never imported from one, and never served by the workspace
// preview origin, which may call the daemon API. Only the sandboxed render
// route reads them, through OpenRender.
func renderName(renderID string) string { return "render-" + renderID + ".html" }

func validateRenderID(renderID string) error {
	if len(renderID) > 200 || !validNamePart(renderID, false) {
		return fmt.Errorf("invalid render id %q", renderID)
	}
	return nil
}

// PutRender stores a render's bytes. Unlike Put it touches no worktree.
func (s *Store) PutRender(ctx context.Context, id domain.SessionID, renderID string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSessionID(id); err != nil {
		return err
	}
	if err := validateRenderID(renderID); err != nil {
		return err
	}
	return s.writeCanonical(ctx, id, renderName(renderID), data)
}

// OpenRender opens a stored render for the sandboxed render route.
func (s *Store) OpenRender(ctx context.Context, id domain.SessionID, renderID string) (*os.File, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if s.dataDir == "" || validateSessionID(id) != nil || validateRenderID(renderID) != nil {
		return nil, nil, fs.ErrNotExist
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, false)
	if err != nil {
		return nil, nil, fs.ErrNotExist
	}
	defer func() { _ = sessionRoot.Close() }()
	return openRegularFile(ctx, sessionRoot, renderName(renderID))
}

// RemoveRender deletes a render whose timeline row was never recorded.
func (s *Store) RemoveRender(ctx context.Context, id domain.SessionID, renderID string) error {
	if s.dataDir == "" || validateSessionID(id) != nil || validateRenderID(renderID) != nil {
		return nil
	}
	sessionRoot, err := s.openCanonicalSession(ctx, id, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = sessionRoot.Close() }()
	if err := sessionRoot.Remove(renderName(renderID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
