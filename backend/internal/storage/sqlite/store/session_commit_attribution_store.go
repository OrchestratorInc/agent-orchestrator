package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SetSessionCommitAttribution records the co-author trailer a session started
// with, so a recreated worktree of that session gets the same one back.
func (s *Store) SetSessionCommitAttribution(ctx context.Context, id domain.SessionID, coAuthor string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionCommitAttribution(ctx, gen.UpsertSessionCommitAttributionParams{SessionID: string(id), CoAuthor: coAuthor}); err != nil {
		return fmt.Errorf("record commit attribution for session %s: %w", id, err)
	}
	return nil
}

// SessionCommitAttribution returns the co-author trailer a session started
// with, or "" when it started without one.
func (s *Store) SessionCommitAttribution(ctx context.Context, id domain.SessionID) (string, error) {
	coAuthor, err := s.qr.GetSessionCommitAttribution(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read commit attribution for session %s: %w", id, err)
	}
	return coAuthor, nil
}
