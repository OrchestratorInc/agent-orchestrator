package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) UpsertSessionCleanupFacts(ctx context.Context, rec domain.SessionCleanupRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	disposition := rec.WorkspaceDisposition
	if disposition == "" {
		disposition = domain.DispositionPending
	}
	return s.qw.UpsertSessionCleanupFacts(ctx, gen.UpsertSessionCleanupFactsParams{
		SessionID:            rec.SessionID,
		SessionGeneration:    rec.SessionGeneration,
		RuntimeReleasedAt:    timeToNullTime(rec.RuntimeReleasedAt),
		WorkspaceDisposition: string(disposition),
		AttemptCount:         rec.AttemptCount,
		LastAttemptAt:        timeToNullTime(rec.LastAttemptAt),
		NextAttemptAt:        timeToNullTime(rec.NextAttemptAt),
		FailureCode:          rec.FailureCode,
		ReportOnly:           rec.ReportOnly,
	})
}

func (s *Store) GetSessionCleanupFacts(ctx context.Context, id domain.SessionID) (domain.SessionCleanupRecord, bool, error) {
	row, err := s.qr.GetSessionCleanupFacts(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionCleanupRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionCleanupRecord{}, false, fmt.Errorf("get session cleanup facts %s: %w", id, err)
	}
	return sessionCleanupFromGen(row), true, nil
}

func (s *Store) ListSessionCleanupFactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.SessionCleanupRecord, error) {
	out := make(map[domain.SessionID]domain.SessionCleanupRecord, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListSessionCleanupFactsBySessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list session cleanup facts: %w", err)
	}
	for _, row := range rows {
		out[row.SessionID] = sessionCleanupFromGen(row)
	}
	return out, nil
}

const listTerminalCleanupCandidatesSQL = `
SELECT s.id
FROM sessions s
LEFT JOIN session_cleanup_facts f ON f.session_id = s.id
WHERE s.is_terminated = 1
  AND (
      f.session_id IS NULL
      OR f.session_generation < s.cleanup_generation
      OR (
          f.workspace_disposition = 'pending'
          AND (f.next_attempt_at IS NULL OR f.next_attempt_at <= ?)
      )
  )
ORDER BY s.project_id, s.num;`

func (s *Store) ListTerminalCleanupCandidates(ctx context.Context, now time.Time) ([]domain.SessionID, error) {
	rows, err := s.readDB.QueryContext(ctx, listTerminalCleanupCandidatesSQL, timeToNullTime(now))
	if err != nil {
		return nil, fmt.Errorf("list terminal cleanup candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.SessionID
	for rows.Next() {
		var id domain.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan terminal cleanup candidate: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list terminal cleanup candidates: %w", err)
	}
	return ids, nil
}

func sessionCleanupFromGen(row gen.SessionCleanupFact) domain.SessionCleanupRecord {
	return domain.SessionCleanupRecord{
		SessionID:            row.SessionID,
		SessionGeneration:    row.SessionGeneration,
		RuntimeReleasedAt:    nullTimeToTime(row.RuntimeReleasedAt),
		WorkspaceDisposition: domain.WorkspaceDisposition(row.WorkspaceDisposition),
		AttemptCount:         row.AttemptCount,
		LastAttemptAt:        nullTimeToTime(row.LastAttemptAt),
		NextAttemptAt:        nullTimeToTime(row.NextAttemptAt),
		FailureCode:          row.FailureCode,
		ReportOnly:           row.ReportOnly,
	}
}
