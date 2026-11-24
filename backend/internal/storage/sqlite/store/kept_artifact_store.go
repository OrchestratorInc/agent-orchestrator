package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// KeepSessionArtifacts records a session's artifacts as kept, with the
// session's project and title, before the session is deleted. Recording them
// again replaces the earlier record. The record is only listed once the
// session row is gone, so a delete that then fails leaves nothing in view.
func (s *Store) KeepSessionArtifacts(ctx context.Context, kept domain.KeptArtifactSession) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "keep session artifacts", func(q *gen.Queries) error {
		if err := q.UpsertKeptArtifactSession(ctx, gen.UpsertKeptArtifactSessionParams{
			SessionID: string(kept.SessionID),
			ProjectID: sql.NullString{String: string(kept.ProjectID), Valid: kept.ProjectID != ""},
			Title:     kept.Title,
			KeptAt:    kept.KeptAt,
		}); err != nil {
			return err
		}
		if err := q.ClearKeptArtifacts(ctx, string(kept.SessionID)); err != nil {
			return err
		}
		return q.CopySessionArtifactsToKept(ctx, kept.SessionID)
	})
	if err != nil {
		return fmt.Errorf("keep artifacts of %s: %w", kept.SessionID, err)
	}
	return nil
}

// ListKeptArtifactSessions returns the deleted sessions whose artifacts are
// kept, most recently deleted first.
func (s *Store) ListKeptArtifactSessions(ctx context.Context) ([]domain.KeptArtifactSession, error) {
	rows, err := s.qr.ListKeptArtifactSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list kept artifact sessions: %w", err)
	}
	out := make([]domain.KeptArtifactSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.KeptArtifactSession{
			SessionID: domain.SessionID(row.SessionID), ProjectID: domain.ProjectID(row.ProjectID), Title: row.Title, KeptAt: row.KeptAt,
		})
	}
	return out, nil
}

// GetKeptArtifactSession returns the kept record of a deleted session, and
// false when there is none or the session still exists.
func (s *Store) GetKeptArtifactSession(ctx context.Context, id domain.SessionID) (domain.KeptArtifactSession, bool, error) {
	row, err := s.qr.GetKeptArtifactSession(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.KeptArtifactSession{}, false, nil
	}
	if err != nil {
		return domain.KeptArtifactSession{}, false, fmt.Errorf("get kept artifact session %s: %w", id, err)
	}
	return domain.KeptArtifactSession{
		SessionID: domain.SessionID(row.SessionID), ProjectID: domain.ProjectID(row.ProjectID), Title: row.Title, KeptAt: row.KeptAt,
	}, true, nil
}

// ListKeptArtifacts returns the artifact rows kept with a deleted session, by
// path, in the form a session's own rows take.
func (s *Store) ListKeptArtifacts(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactRecord, error) {
	rows, err := s.qr.ListKeptArtifacts(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list kept artifacts of %s: %w", id, err)
	}
	out := make([]domain.SessionArtifactRecord, 0, len(rows))
	for _, row := range rows {
		record := domain.SessionArtifactRecord{ID: row.ID, SessionID: id, Path: row.Path, CreatedAt: row.CreatedAt}
		if row.RegisteredAt.Valid {
			registeredAt := row.RegisteredAt.Time
			record.RegisteredAt = &registeredAt
		}
		if json.Unmarshal([]byte(row.PreviousPaths), &record.PreviousPaths) != nil {
			record.PreviousPaths = nil
		}
		out = append(out, record)
	}
	return out, nil
}

// RemoveKeptArtifact forgets the kept artifact at path after its file was deleted.
func (s *Store) RemoveKeptArtifact(ctx context.Context, id domain.SessionID, path string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteKeptArtifact(ctx, gen.DeleteKeptArtifactParams{SessionID: string(id), Path: path}); err != nil {
		return fmt.Errorf("remove kept artifact of %s: %w", id, err)
	}
	return nil
}

// ForgetKeptArtifactSession drops a session's kept record and its rows.
func (s *Store) ForgetKeptArtifactSession(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteKeptArtifactSession(ctx, string(id)); err != nil {
		return fmt.Errorf("forget kept artifacts of %s: %w", id, err)
	}
	return nil
}

// ListRemovedProjectSessionIDs returns the sessions of removed projects. Their
// rows stay, but no project lists them.
func (s *Store) ListRemovedProjectSessionIDs(ctx context.Context) ([]domain.SessionID, error) {
	ids, err := s.qr.ListRemovedProjectSessionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list removed project sessions: %w", err)
	}
	return ids, nil
}
