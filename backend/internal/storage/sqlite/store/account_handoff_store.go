package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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
