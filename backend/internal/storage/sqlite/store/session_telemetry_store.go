package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) RecordSessionCreatedBy(ctx context.Context, id domain.SessionID, createdBy domain.SessionCreator) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.RecordSessionCreatedBy(ctx, gen.RecordSessionCreatedByParams{SessionID: string(id), CreatedBy: string(createdBy)}); err != nil {
		return fmt.Errorf("record created_by for session %s: %w", id, err)
	}
	return nil
}

func (s *Store) IncrementSessionWaitingInputCount(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.IncrementSessionWaitingInputCount(ctx, string(id)); err != nil {
		return fmt.Errorf("count waiting input for session %s: %w", id, err)
	}
	return nil
}

func (s *Store) GetSessionTelemetry(ctx context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error) {
	row, err := s.qr.GetSessionTelemetry(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionTelemetryFacts{}, nil
	}
	if err != nil {
		return domain.SessionTelemetryFacts{}, fmt.Errorf("get telemetry facts for session %s: %w", id, err)
	}
	return domain.SessionTelemetryFacts{CreatedBy: domain.SessionCreator(row.CreatedBy), WaitingInputCount: row.WaitingInputCount}, nil
}

func (s *Store) ClaimSessionEndTelemetry(ctx context.Context, id domain.SessionID) (bool, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	_, err := s.qw.ClaimSessionEndTelemetry(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim end telemetry for session %s: %w", id, err)
	}
	return true, nil
}
