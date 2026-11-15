package attachmentstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func renderName(renderID string) string { return "render-" + renderID + ".html" }

func validateRenderID(renderID string) error {
	if len(renderID) > 200 || !validNamePart(renderID, false) {
		return fmt.Errorf("invalid render id %q", renderID)
	}
	return nil
}

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
