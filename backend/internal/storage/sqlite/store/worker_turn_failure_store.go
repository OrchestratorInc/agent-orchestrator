package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// EnqueueWorkerTurnFailure participates in the provider-event projection
// transaction when called from a Chat controller. The unique turn key also
// protects against a replay after a lost provider acknowledgement.
func (s *Store) EnqueueWorkerTurnFailure(ctx context.Context, conversationID, providerTurnID string, at time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.EnqueueWorkerTurnFailure(ctx, gen.EnqueueWorkerTurnFailureParams{
		CreatedAt: at, ConversationID: conversationID, ProviderTurnID: providerTurnID,
	}); err != nil {
		return fmt.Errorf("enqueue worker turn failure: %w", err)
	}
	return nil
}

// ListDueWorkerTurnFailures reads failures ready for semantic delivery.
func (s *Store) ListDueWorkerTurnFailures(ctx context.Context, at time.Time, limit int64) ([]domain.WorkerTurnFailure, error) {
	rows, err := s.qr.ListDueWorkerTurnFailures(ctx, gen.ListDueWorkerTurnFailuresParams{Now: at, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]domain.WorkerTurnFailure, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.WorkerTurnFailure{
			TurnID: row.TurnID, SessionID: domain.SessionID(row.SessionID),
			ProjectID: domain.ProjectID(row.ProjectID), DisplayName: row.DisplayName,
			ErrorMessage: row.ErrorMessage, Attempts: row.Attempts,
		})
	}
	return out, nil
}

// AcknowledgeWorkerTurnFailure records semantic acceptance.
func (s *Store) AcknowledgeWorkerTurnFailure(ctx context.Context, turnID string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.AcknowledgeWorkerTurnFailure(ctx, gen.AcknowledgeWorkerTurnFailureParams{
		AcceptedAt: nullableTime(at), TurnID: turnID,
	})
	return err
}

// RetryWorkerTurnFailure defers an unaccepted delivery with a durable deadline.
func (s *Store) RetryWorkerTurnFailure(ctx context.Context, turnID string, next time.Time, reason string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.RetryWorkerTurnFailure(ctx, gen.RetryWorkerTurnFailureParams{
		NextAttemptAt: next, LastError: reason, TurnID: turnID,
	})
	return err
}

// HasSettledFailedPrimaryTurn permits recovery only when the latest non-queued
// Chat turn failed and no approval or structured input still needs a decision.
func (s *Store) HasSettledFailedPrimaryTurn(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	return s.qr.HasSettledFailedPrimaryTurn(ctx, &sessionID)
}
