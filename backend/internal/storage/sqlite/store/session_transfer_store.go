package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

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

// SessionMovedTo reports where a moved session continues.
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
