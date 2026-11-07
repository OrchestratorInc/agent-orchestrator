package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SessionAccountPinned reports the session's "Stay on this account" choice.
func (s *Store) SessionAccountPinned(ctx context.Context, id domain.SessionID) (bool, error) {
	n, err := s.qr.GetSessionAccountPin(ctx, string(id))
	if err != nil {
		return false, fmt.Errorf("read session account pin %s: %w", id, err)
	}
	return n > 0, nil
}

// SetSessionAccountPinned records or clears "Stay on this account".
func (s *Store) SetSessionAccountPinned(ctx context.Context, id domain.SessionID, pinned bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.GetSession(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return ports.ErrSessionNotFound
	} else if err != nil {
		return fmt.Errorf("read session %s: %w", id, err)
	}
	var err error
	if pinned {
		err = s.qw.PinSessionAccount(ctx, gen.PinSessionAccountParams{SessionID: string(id), PinnedAt: now})
	} else {
		err = s.qw.UnpinSessionAccount(ctx, string(id))
	}
	if err != nil {
		return fmt.Errorf("set session account pin %s: %w", id, err)
	}
	return nil
}

// AccountHandoffCandidateTurns lists recent failed chat turns that have no retry yet.
func (s *Store) AccountHandoffCandidateTurns(ctx context.Context, since time.Time) ([]domain.ConversationTurn, error) {
	rows, err := s.qr.SelectAccountHandoffCandidateTurns(ctx, timeToNullTime(since))
	if err != nil {
		return nil, fmt.Errorf("list account handoff candidates: %w", err)
	}
	turns := make([]domain.ConversationTurn, 0, len(rows))
	for _, row := range rows {
		turns = append(turns, turnToDomain(row))
	}
	return turns, nil
}
