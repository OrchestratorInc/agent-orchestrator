package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// InsertReminder persists one pending reminder unless its session already has
// limit pending ones. The count and insert share a transaction so concurrent
// ao remind calls cannot overshoot the cap.
func (s *Store) InsertReminder(ctx context.Context, r domain.Reminder, limit int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	pending, err := q.CountPendingSessionReminders(ctx, r.SessionID)
	if err != nil {
		return fmt.Errorf("count reminders for %s: %w", r.SessionID, err)
	}
	if limit > 0 && pending >= int64(limit) {
		return domain.ErrReminderLimit
	}
	err = q.InsertSessionReminder(ctx, gen.InsertSessionReminderParams{
		ID:        r.ID,
		SessionID: r.SessionID,
		ProjectID: r.ProjectID,
		Name:      r.Name,
		Message:   r.Message,
		DueAt:     r.DueAt.UTC(),
		CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("insert reminder %s: %w", r.ID, err)
	}
	return tx.Commit()
}

// GetReminder looks up one reminder by id.
func (s *Store) GetReminder(ctx context.Context, id domain.ReminderID) (domain.Reminder, bool, error) {
	row, err := s.qr.SelectSessionReminderByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Reminder{}, false, nil
	}
	if err != nil {
		return domain.Reminder{}, false, fmt.Errorf("select reminder %s: %w", id, err)
	}
	return reminderFromGen(row), true, nil
}

// ListPendingReminders returns one session's pending reminders, soonest first.
func (s *Store) ListPendingReminders(ctx context.Context, sessionID domain.SessionID) ([]domain.Reminder, error) {
	rows, err := s.qr.SelectPendingSessionReminders(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("select reminders for %s: %w", sessionID, err)
	}
	return remindersFromGen(rows), nil
}

// ListDueReminders returns up to limit pending reminders due at or before at.
func (s *Store) ListDueReminders(ctx context.Context, at time.Time, limit int64) ([]domain.Reminder, error) {
	rows, err := s.qr.SelectDueSessionReminders(ctx, gen.SelectDueSessionRemindersParams{DueAt: at.UTC(), Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("select due reminders: %w", err)
	}
	return remindersFromGen(rows), nil
}

// ResolveReminder moves a pending reminder to a final status. It reports
// false when the reminder was no longer pending, so exactly one of a cancel
// and a delivery wins.
func (s *Store) ResolveReminder(ctx context.Context, id domain.ReminderID, status domain.ReminderStatus, reason string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	at = at.UTC()
	affected, err := s.qw.ResolveSessionReminder(ctx, gen.ResolveSessionReminderParams{
		Status: status, StatusReason: reason, ResolvedAt: sql.NullTime{Time: at, Valid: true}, UpdatedAt: at, ID: id,
	})
	if err != nil {
		return false, fmt.Errorf("resolve reminder %s: %w", id, err)
	}
	return affected > 0, nil
}

// DeferReminder pushes a pending reminder's delivery to dueAt and records why.
func (s *Store) DeferReminder(ctx context.Context, id domain.ReminderID, dueAt time.Time, reason string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.DeferSessionReminder(ctx, gen.DeferSessionReminderParams{
		DueAt: dueAt.UTC(), StatusReason: reason, UpdatedAt: at.UTC(), ID: id,
	})
	if err != nil {
		return false, fmt.Errorf("defer reminder %s: %w", id, err)
	}
	return affected > 0, nil
}

// CountRemindersSinceHumanMessage counts the agent's reminders delivered to a
// session since a human last wrote in it.
func (s *Store) CountRemindersSinceHumanMessage(ctx context.Context, sessionID domain.SessionID) (int, error) {
	n, err := s.qr.CountRemindersDeliveredSinceHumanMessage(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("count delivered reminders for %s: %w", sessionID, err)
	}
	return int(n), nil
}

func remindersFromGen(rows []gen.SessionReminder) []domain.Reminder {
	out := make([]domain.Reminder, 0, len(rows))
	for _, row := range rows {
		out = append(out, reminderFromGen(row))
	}
	return out
}

func reminderFromGen(row gen.SessionReminder) domain.Reminder {
	r := domain.Reminder{
		ID:           row.ID,
		SessionID:    row.SessionID,
		ProjectID:    row.ProjectID,
		Name:         row.Name,
		Message:      row.Message,
		DueAt:        row.DueAt.UTC(),
		CreatedBy:    row.CreatedBy,
		Status:       row.Status,
		StatusReason: row.StatusReason,
		Attempts:     row.Attempts,
		CreatedAt:    row.CreatedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
	}
	if row.ResolvedAt.Valid {
		at := row.ResolvedAt.Time.UTC()
		r.ResolvedAt = &at
	}
	return r
}
