package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) EventsAfter(ctx context.Context, after int64, limit int) ([]cdc.Event, error) {
	rows, err := s.qr.ReadChangeLogAfter(ctx, gen.ReadChangeLogAfterParams{Seq: after, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("read change_log after %d: %w", after, err)
	}
	events := make([]cdc.Event, 0, len(rows))
	for _, r := range rows {
		events = append(events, changeLogEventFromGen(r))
	}
	return events, nil
}

func (s *Store) LatestSeq(ctx context.Context) (int64, error) {
	seq, err := s.qr.MaxChangeLogSeq(ctx)
	if err != nil {
		return 0, fmt.Errorf("max change_log seq: %w", err)
	}
	return seq, nil
}

func (s *Store) PruneChangeLogBefore(ctx context.Context, before time.Time, limit int64) (int64, error) {
	n, err := s.qw.PruneChangeLogBefore(ctx, gen.PruneChangeLogBeforeParams{
		CreatedAt: before.UTC(),
		Limit:     limit,
	})
	if err != nil {
		return 0, fmt.Errorf("prune change_log before %s: %w", before.UTC().Format(time.RFC3339), err)
	}
	return n, nil
}

func (s *Store) PruneChangeLogToMaxRows(ctx context.Context, maxRows, limit int64) (int64, error) {
	n, err := s.qw.PruneChangeLogToMaxRows(ctx, gen.PruneChangeLogToMaxRowsParams{
		BatchLimit: limit,
		MaxRows:    maxRows,
	})
	if err != nil {
		return 0, fmt.Errorf("prune change_log to %d rows: %w", maxRows, err)
	}
	return n, nil
}

func changeLogEventFromGen(r gen.ChangeLog) cdc.Event {
	e := cdc.Event{
		Seq:       r.Seq,
		ProjectID: string(projectIDValue(r.ProjectID)),
		Type:      r.EventType,
		Payload:   json.RawMessage(r.Payload),
		CreatedAt: r.CreatedAt,
	}
	if r.SessionID != nil {
		e.SessionID = string(*r.SessionID)
	}
	return e
}
