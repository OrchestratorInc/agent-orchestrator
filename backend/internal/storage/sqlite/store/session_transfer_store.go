package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// RecordPendingTransfer marks a session as being moved to dest. It replaces an
// earlier pending move and leaves a finished one alone.
func (s *Store) RecordPendingTransfer(ctx context.Context, id domain.SessionID, dest domain.SessionMovedTo, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.qw.RecordPendingTransfer(ctx, gen.RecordPendingTransferParams{
		SessionID:  string(id),
		TransferID: dest.TransferID,
		DestHostID: dest.HostID,
		DestLabel:  dest.Label,
		CreatedAt:  at.UTC(),
	})
}

// CancelPendingTransfer forgets an unfinished move.
func (s *Store) CancelPendingTransfer(ctx context.Context, id domain.SessionID, transferID string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.qw.CancelPendingTransfer(ctx, gen.CancelPendingTransferParams{SessionID: string(id), TransferID: transferID})
}

// RecordSessionTransfer marks a session as moved to another machine. A second
// record for the same session keeps the first.
func (s *Store) RecordSessionTransfer(ctx context.Context, id domain.SessionID, moved domain.SessionMovedTo, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.qw.RecordSessionTransfer(ctx, gen.RecordSessionTransferParams{
		SessionID:     string(id),
		TransferID:    moved.TransferID,
		DestHostID:    moved.HostID,
		DestLabel:     moved.Label,
		DestSessionID: string(moved.SessionID),
		CreatedAt:     at.UTC(),
	})
}

// SessionMovedTo reports where a moved session continues, or is being moved
// to when the move has not finished.
func (s *Store) SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error) {
	row, err := s.qr.GetSessionTransfer(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionMovedTo{}, false, nil
	}
	if err != nil {
		return domain.SessionMovedTo{}, false, err
	}
	return domain.SessionMovedTo{
		TransferID: row.TransferID,
		HostID:     row.DestHostID,
		Label:      row.DestLabel,
		SessionID:  domain.SessionID(row.DestSessionID),
	}, true, nil
}

// ListMovedSessionIDs lists sessions that moved to another machine.
func (s *Store) ListMovedSessionIDs(ctx context.Context) (map[domain.SessionID]bool, error) {
	ids, err := s.qr.ListTransferredSessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.SessionID]bool, len(ids))
	for _, id := range ids {
		out[domain.SessionID(id)] = true
	}
	return out, nil
}
