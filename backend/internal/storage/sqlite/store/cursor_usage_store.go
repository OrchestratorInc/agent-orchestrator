package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CursorUsageEnabled reports whether Cursor usage is read; an unreadable
// setting counts as off.
func (s *Store) CursorUsageEnabled(ctx context.Context) bool {
	enabled, err := s.qr.GetCursorUsage(ctx)
	return err == nil && enabled
}

// SetCursorUsage turns reading Cursor usage on or off.
func (s *Store) SetCursorUsage(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetCursorUsage(ctx, gen.SetCursorUsageParams{CursorUsage: enabled, UpdatedAt: now.UTC()}); err != nil {
		return fmt.Errorf("set cursor usage: %w", err)
	}
	return nil
}

// GetUsageCursorRead returns the account Cursor usage was last read for and
// how far; ok is false before the first read.
func (s *Store) GetUsageCursorRead(ctx context.Context) (accountKey string, until time.Time, ok bool, err error) {
	row, err := s.qr.GetUsageCursorRead(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return row.AccountKey, row.ReadUntil, true, nil
}

// ReplaceCursorUsage stores one Cursor read: the account's stored events from
// Since to Until become read.Events, and the read mark moves to Until.
func (s *Store) ReplaceCursorUsage(ctx context.Context, read domain.CursorUsageRead) error {
	if strings.TrimSpace(read.AccountKey) == "" {
		return errors.New("replace cursor usage: no account")
	}
	const layout = "2006-01-02 15:04:05"
	path := "cursor-dashboard:" + read.AccountKey
	now := timeOrNow(read.Now)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "replace cursor usage", func(q *gen.Queries) error {
		if err := q.DeleteCursorUsageEventsInRange(ctx, gen.DeleteCursorUsageEventsInRangeParams{
			ArtifactPath: path,
			SinceUtc:     read.Since.UTC().Format(layout),
			UntilUtc:     read.Until.UTC().Format(layout),
		}); err != nil {
			return err
		}
		sources := map[string]gen.GetUsageSourceWithBindingAndSessionRow{}
		for _, item := range read.Events {
			source, ok := sources[item.ConversationID]
			if !ok {
				var err error
				if source, err = cursorUsageSource(ctx, q, item, path, now); err != nil {
					return err
				}
				sources[item.ConversationID] = source
			}
			event := item.Event
			if err := validateUsageEvent(domain.HarnessCursor, event); err != nil {
				return err
			}
			if _, err := q.InsertModelUsageEvent(ctx, usageEventInsertParams(source, event)); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := q.DeleteUsageCursorReads(ctx); err != nil {
			return err
		}
		return q.InsertUsageCursorRead(ctx, gen.InsertUsageCursorReadParams{AccountKey: read.AccountKey, ReadUntil: read.Until.UTC()})
	})
}

// cursorUsageSource finds or makes the binding and source a Cursor
// conversation's events belong to. Both stay complete: there is no file for
// the collector to drain.
func cursorUsageSource(ctx context.Context, q *gen.Queries, item domain.CursorSessionEvent, path string, now time.Time) (gen.GetUsageSourceWithBindingAndSessionRow, error) {
	if strings.TrimSpace(item.ConversationID) == "" {
		return gen.GetUsageSourceWithBindingAndSessionRow{}, errors.New("cursor usage event has no conversation")
	}
	binding, err := q.GetUsageBindingBySessionHarnessRoot(ctx, gen.GetUsageBindingBySessionHarnessRootParams{
		SessionID: item.SessionID, Harness: domain.HarnessCursor, NativeRootID: item.ConversationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		binding, err = q.UpsertUsageBinding(ctx, gen.UpsertUsageBindingParams{
			SessionID: item.SessionID, Harness: domain.HarnessCursor, NativeRootID: item.ConversationID,
			State: domain.UsageBindingComplete, UpdatedAt: now,
		})
	}
	if err != nil {
		return gen.GetUsageSourceWithBindingAndSessionRow{}, fmt.Errorf("cursor usage binding for session %s: %w", item.SessionID, err)
	}
	source, err := q.InsertUsageSource(ctx, usageSourceInsertParams(domain.UsageSourceRecord{
		BindingID: binding.ID, Kind: domain.UsageSourceCursorDashboard, NativeSessionID: item.ConversationID,
		ArtifactPath: path, ParserStateJSON: "{}", State: domain.UsageSourceComplete, UpdatedAt: now,
	}))
	if err != nil {
		return gen.GetUsageSourceWithBindingAndSessionRow{}, fmt.Errorf("cursor usage source for session %s: %w", item.SessionID, err)
	}
	return gen.GetUsageSourceWithBindingAndSessionRow{BindingID: binding.ID, SourceID: source.ID}, nil
}
