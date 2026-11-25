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

// SetSessionPRFooter records whether a session started with the Orchestrator
// PR footer, so its prompt keeps that choice when it is rebuilt later.
func (s *Store) SetSessionPRFooter(ctx context.Context, id domain.SessionID, footer bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	value := sql.NullInt64{Valid: true}
	if footer {
		value.Int64 = 1
	}
	if err := s.qw.UpsertSessionPRFooter(ctx, gen.UpsertSessionPRFooterParams{SessionID: string(id), PRFooter: value}); err != nil {
		return fmt.Errorf("record PR footer choice for session %s: %w", id, err)
	}
	return nil
}

// SessionPRFooter returns the PR footer choice a session started with.
// recorded is false for a session with no recorded choice: one started before
// the choice was kept, whose prompt follows the current settings.
func (s *Store) SessionPRFooter(ctx context.Context, id domain.SessionID) (footer, recorded bool, err error) {
	value, err := s.qr.GetSessionPRFooter(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read PR footer choice for session %s: %w", id, err)
	}
	return value.Valid && value.Int64 == 1, value.Valid, nil
}
