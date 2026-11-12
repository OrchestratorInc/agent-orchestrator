package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// GetSessionAdditionalInstructions reads the session-wide next-message policy.
func (s *Store) GetSessionAdditionalInstructions(ctx context.Context, id domain.SessionID) (string, error) {
	text, err := s.qr.GetSessionAdditionalInstructions(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ports.ErrSessionNotFound
	}
	return text, err
}

// SetSessionAdditionalInstructions serializes a narrow write so stale session saves cannot undo it.
func (s *Store) SetSessionAdditionalInstructions(ctx context.Context, id domain.SessionID, text string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.SetSessionAdditionalInstructions(ctx, gen.SetSessionAdditionalInstructionsParams{ID: id, AdditionalInstructions: text})
	if err != nil {
		return err
	}
	if n == 0 {
		return ports.ErrSessionNotFound
	}
	return nil
}
