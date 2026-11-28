package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.SessionVersionMoveStore = (*Store)(nil)

// PutSessionVersionMove records pre-stop intent without resetting an existing move.
func (s *Store) PutSessionVersionMove(ctx context.Context, move domain.SessionVersionMove) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	intent, err := json.Marshal(move)
	if err != nil {
		return err
	}
	n, err := s.qw.PutSessionVersionMove(ctx, gen.PutSessionVersionMoveParams{SessionID: string(move.SessionID), Intent: string(intent)})
	if err == nil && n == 0 {
		return fmt.Errorf("session %s already has a version move; resume it first", move.SessionID)
	}
	return err
}

// ListSessionVersionMoves returns unfinished moves, including exhausted retries.
func (s *Store) ListSessionVersionMoves(ctx context.Context) ([]domain.SessionVersionMove, error) {
	rows, err := s.qr.ListSessionVersionMoves(ctx)
	if err != nil {
		return nil, err
	}
	moves := make([]domain.SessionVersionMove, 0, len(rows))
	for _, row := range rows {
		var move domain.SessionVersionMove
		if err := json.Unmarshal([]byte(row.Intent), &move); err != nil {
			return nil, fmt.Errorf("decode version move for %s: %w", row.SessionID, err)
		}
		move.SessionID, move.Stage, move.Attempts = domain.SessionID(row.SessionID), row.Stage, int(row.Attempts)
		moves = append(moves, move)
	}
	return moves, nil
}

// SetSessionVersionMoveStage checkpoints an existing move before process changes.
func (s *Store) SetSessionVersionMoveStage(ctx context.Context, id domain.SessionID, stage string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.SetSessionVersionMoveStage(ctx, gen.SetSessionVersionMoveStageParams{SessionID: id, Stage: stage})
}

// AttemptSessionVersionMove durably consumes one retry, up to limit.
func (s *Store) AttemptSessionVersionMove(ctx context.Context, id domain.SessionID, limit int) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.AttemptSessionVersionMove(ctx, gen.AttemptSessionVersionMoveParams{SessionID: string(id), MaxAttempts: int64(limit)})
	return n > 0, err
}

// DeleteSessionVersionMove clears intent after recovery or a controller change.
func (s *Store) DeleteSessionVersionMove(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.DeleteSessionVersionMove(ctx, string(id))
}
