package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

type TelemetryEventRecord struct {
	ID          string
	OccurredAt  time.Time
	Name        string
	Source      string
	Level       string
	ProjectID   *domain.ProjectID
	SessionID   *domain.SessionID
	RequestID   string
	PayloadJSON string
}

func (s *Store) CreateTelemetryEvent(ctx context.Context, rec TelemetryEventRecord) error {
	arg := gen.CreateTelemetryEventParams{
		ID:          rec.ID,
		OccurredAt:  rec.OccurredAt.UTC(),
		Name:        rec.Name,
		Source:      rec.Source,
		Level:       rec.Level,
		RequestID:   rec.RequestID,
		PayloadJson: rec.PayloadJSON,
	}
	if rec.ProjectID != nil {
		arg.ProjectID = sql.NullString{String: string(*rec.ProjectID), Valid: true}
	}
	if rec.SessionID != nil {
		arg.SessionID = sql.NullString{String: string(*rec.SessionID), Valid: true}
	}
	if err := s.qw.CreateTelemetryEvent(ctx, arg); err != nil {
		return fmt.Errorf("create telemetry event %s: %w", rec.ID, err)
	}
	return nil
}

func (s *Store) ListTelemetryEventsSince(ctx context.Context, since time.Time, limit int64) ([]gen.TelemetryEvent, error) {
	rows, err := s.qr.ListTelemetryEventsSince(ctx, gen.ListTelemetryEventsSinceParams{
		OccurredAt: since.UTC(),
		Limit:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list telemetry events since %s: %w", since.UTC().Format(time.RFC3339), err)
	}
	return rows, nil
}

func (s *Store) PruneTelemetryEventsBefore(ctx context.Context, before time.Time, limit int64) (int64, error) {
	n, err := s.qw.PruneTelemetryEventsBefore(ctx, gen.PruneTelemetryEventsBeforeParams{
		OccurredAt: before.UTC(),
		Limit:      limit,
	})
	if err != nil {
		return 0, fmt.Errorf("prune telemetry events before %s: %w", before.UTC().Format(time.RFC3339), err)
	}
	return n, nil
}

func (s *Store) FreelistCount(ctx context.Context) (int64, error) {
	var n int64
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&n); err != nil {
		return 0, fmt.Errorf("freelist_count: %w", err)
	}
	return n, nil
}

func (s *Store) IncrementalVacuum(ctx context.Context, pages int64) error {
	if _, err := s.writeDB.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages)); err != nil {
		return fmt.Errorf("incremental_vacuum: %w", err)
	}
	return nil
}
