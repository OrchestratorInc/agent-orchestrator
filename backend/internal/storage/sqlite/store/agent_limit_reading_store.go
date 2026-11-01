package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ ports.AgentLimitReadingStore = (*Store)(nil)

// limitReadingRetention is how long limit readings are kept: the usage page
// reads one week, and a weekly window can open up to a week before that.
const limitReadingRetention = 14 * 24 * time.Hour

// RecordAgentLimitReadings saves one read of an account's limit windows and
// deletes readings older than the retention.
func (s *Store) RecordAgentLimitReadings(ctx context.Context, readings []domain.AgentLimitReading) error {
	if len(readings) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "record agent limit readings", func(q *gen.Queries) error {
		for _, r := range readings {
			resets := sql.NullInt64{}
			if r.ResetsAt != nil {
				resets = sql.NullInt64{Int64: r.ResetsAt.UnixMilli(), Valid: true}
			}
			if err := q.InsertAgentLimitReading(ctx, gen.InsertAgentLimitReadingParams{
				AccountID: r.AccountID, Provider: string(r.Provider), Email: r.Email, Plan: r.Plan, WindowID: r.WindowID, Label: r.Label,
				DurationMinutes: int64(r.DurationMinutes), UsedPercent: r.UsedPercent, ResetsAtMs: resets, ReadAtMs: r.ReadAt.UnixMilli(),
			}); err != nil {
				return fmt.Errorf("insert %s %s: %w", r.AccountID, r.WindowID, err)
			}
		}
		return q.DeleteAgentLimitReadingsBefore(ctx, readings[0].ReadAt.Add(-limitReadingRetention).UnixMilli())
	})
}

// ListAgentLimitReadings returns readings taken at or after since.
func (s *Store) ListAgentLimitReadings(ctx context.Context, since time.Time) ([]domain.AgentLimitReading, error) {
	rows, err := s.qr.ListAgentLimitReadingsSince(ctx, since.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list agent limit readings: %w", err)
	}
	out := make([]domain.AgentLimitReading, 0, len(rows))
	for _, row := range rows {
		r := domain.AgentLimitReading{
			AccountID: row.AccountID, Provider: domain.AgentAccountProvider(row.Provider), Email: row.Email, Plan: row.Plan, WindowID: row.WindowID,
			Label: row.Label, DurationMinutes: int(row.DurationMinutes), UsedPercent: row.UsedPercent, ReadAt: time.UnixMilli(row.ReadAtMs).UTC(),
		}
		if row.ResetsAtMs.Valid {
			at := time.UnixMilli(row.ResetsAtMs.Int64).UTC()
			r.ResetsAt = &at
		}
		out = append(out, r)
	}
	return out, nil
}

// LimitHistoryEnabled reports whether limit readings are kept; an unreadable
// setting counts as off.
func (s *Store) LimitHistoryEnabled(ctx context.Context) bool {
	enabled, err := s.qr.GetLimitHistory(ctx)
	return err == nil && enabled
}

// SetLimitHistory turns keeping limit readings on or off.
func (s *Store) SetLimitHistory(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetLimitHistory(ctx, gen.SetLimitHistoryParams{LimitHistory: enabled, UpdatedAt: now.UTC()}); err != nil {
		return fmt.Errorf("set limit history: %w", err)
	}
	return nil
}
