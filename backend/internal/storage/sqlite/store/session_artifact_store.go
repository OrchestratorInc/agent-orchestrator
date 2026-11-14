package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func (s *Store) AddSessionArtifacts(ctx context.Context, id domain.SessionID, registrations []domain.SessionArtifactRegistration, registeredAt time.Time) ([]string, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var registered []string
	found := true
	err := s.inTx(ctx, "add session artifacts", func(q *gen.Queries) error {
		var err error
		if found, err = q.SessionIDExists(ctx, id); err != nil || !found {
			return err
		}
		for _, registration := range registrations {
			path := registration.Path
			if err := q.InsertSessionArtifact(ctx, gen.InsertSessionArtifactParams{
				ID: uuid.NewString(), SessionID: id, Path: path, CreatedAt: registeredAt,
			}); err != nil {
				return err
			}
			if err := q.RegisterSessionArtifact(ctx, gen.RegisterSessionArtifactParams{
				SessionID: id, Path: path, RegisteredAt: sql.NullTime{Time: registeredAt, Valid: true},
				FileDev: int64(registration.File.Dev), FileIno: int64(registration.File.Ino), FileBirth: registration.File.Birth, FileGeneration: int64(registration.File.Generation), FileMtime: registration.File.ModTime, //nolint:gosec // stored as SQLite's signed integer and read back the same way
			}); err != nil {
				return err
			}
		}
		registered, err = q.ListRegisteredSessionArtifactPaths(ctx, id)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("add session artifacts for %s: %w", id, err)
	}
	return registered, found, nil
}

func (s *Store) ListSessionArtifacts(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactRecord, error) {
	rows, err := s.qr.ListSessionArtifacts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list session artifacts for %s: %w", id, err)
	}
	out := make([]domain.SessionArtifactRecord, 0, len(rows))
	for _, row := range rows {
		record := sessionArtifactRecord(row.SessionArtifact)
		record.HasVersions = row.HasVersions != 0
		out = append(out, record)
	}
	return out, nil
}

func (s *Store) ListSessionArtifactsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionArtifactRecord, error) {
	out := make(map[domain.SessionID][]domain.SessionArtifactRecord)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListSessionArtifactsForSessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list session artifacts: %w", err)
	}
	for _, row := range rows {
		record := sessionArtifactRecord(row.SessionArtifact)
		record.HasVersions = row.HasVersions != 0
		out[record.SessionID] = append(out[record.SessionID], record)
	}
	return out, nil
}

func (s *Store) ApplySessionArtifactChanges(ctx context.Context, id domain.SessionID, changes domain.SessionArtifactChanges) ([]string, error) {
	if changes.Empty() {
		return nil, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var freed []string
	err := s.inTx(ctx, "apply session artifact changes", func(q *gen.Queries) error {
		freed = nil
		for _, drop := range changes.Drop {
			n, err := q.DeleteSessionArtifactByID(ctx, gen.DeleteSessionArtifactByIDParams{ID: drop.ID, Path: drop.Path})
			if err != nil {
				return err
			}
			if n != 1 {
				return domain.ErrSessionArtifactsChanged
			}
			sums, err := q.DeleteSessionArtifactPathVersions(ctx, gen.DeleteSessionArtifactPathVersionsParams{SessionID: id, Path: drop.Path})
			if err != nil {
				return err
			}
			freed = append(freed, sums...)
		}
		for _, move := range changes.Move {
			if err := moveSessionArtifact(ctx, q, id, move.ID, move.From, movingPath(move.ID), nil, domain.FileIdentity{}); err != nil {
				return err
			}
		}
		for _, move := range changes.Move {
			sums, err := q.DeleteSessionArtifactPathVersions(ctx, gen.DeleteSessionArtifactPathVersionsParams{SessionID: id, Path: move.To})
			if err != nil {
				return err
			}
			freed = append(freed, sums...)
			if err := moveSessionArtifact(ctx, q, id, move.ID, movingPath(move.ID), move.To, move.PreviousPaths, move.File); err != nil {
				return err
			}
		}
		for _, update := range changes.SetFile {
			n, err := q.SetSessionArtifactFile(ctx, gen.SetSessionArtifactFileParams{
				ID: update.ID, Path: update.Path,
				FileDev: int64(update.File.Dev), FileIno: int64(update.File.Ino), FileBirth: update.File.Birth, FileGeneration: int64(update.File.Generation), FileMtime: update.File.ModTime, //nolint:gosec // stored as SQLite's signed integer and read back the same way
			})
			if err != nil {
				return err
			}
			if n != 1 {
				return domain.ErrSessionArtifactsChanged
			}
		}
		for _, forget := range changes.Forget {
			n, err := q.ForgetSessionArtifact(ctx, gen.ForgetSessionArtifactParams{ID: forget.ID, Path: forget.Path})
			if err != nil {
				return err
			}
			if n == 1 {
				continue
			}
			if n, err = q.SetSessionArtifactFile(ctx, gen.SetSessionArtifactFileParams{ID: forget.ID, Path: forget.Path}); err != nil {
				return err
			}
			if n != 1 {
				return domain.ErrSessionArtifactsChanged
			}
		}
		for _, row := range changes.Insert {
			if err := q.InsertSessionArtifact(ctx, gen.InsertSessionArtifactParams{
				ID: row.ID, SessionID: id, Path: row.Path, CreatedAt: row.CreatedAt,
				FileDev: int64(row.File.Dev), FileIno: int64(row.File.Ino), FileBirth: row.File.Birth, FileGeneration: int64(row.File.Generation), FileMtime: row.File.ModTime, //nolint:gosec // see above
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrSessionArtifactsChanged) {
			return nil, domain.ErrSessionArtifactsChanged
		}
		return nil, fmt.Errorf("apply session artifact changes for %s: %w", id, err)
	}
	return freed, nil
}

func movingPath(id string) string {
	return "\x00moving/" + id
}

func moveSessionArtifact(ctx context.Context, q *gen.Queries, session domain.SessionID, id, from, to string, previous []string, file domain.FileIdentity) error {
	if previous == nil {
		previous = []string{}
	}
	encoded, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	n, err := q.MoveSessionArtifact(ctx, gen.MoveSessionArtifactParams{
		ID: id, FromPath: from, ToPath: to, PreviousPaths: string(encoded),
		FileDev: int64(file.Dev), FileIno: int64(file.Ino), FileBirth: file.Birth, FileGeneration: int64(file.Generation), FileMtime: file.ModTime, //nolint:gosec // see ApplySessionArtifactChanges
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrSessionArtifactsChanged
	}
	return q.MoveSessionArtifactVersions(ctx, gen.MoveSessionArtifactVersionsParams{SessionID: session, FromPath: from, ToPath: to})
}

func sessionArtifactRecord(row gen.SessionArtifact) domain.SessionArtifactRecord {
	record := domain.SessionArtifactRecord{
		ID:        row.ID,
		SessionID: row.SessionID,
		Path:      row.Path,
		File:      domain.FileIdentity{Dev: uint64(row.FileDev), Ino: uint64(row.FileIno), Birth: row.FileBirth, Generation: uint64(row.FileGeneration), ModTime: row.FileMtime}, //nolint:gosec // written from these uint64 values
		CreatedAt: row.CreatedAt,
	}
	if row.RegisteredAt.Valid {
		registeredAt := row.RegisteredAt.Time
		record.RegisteredAt = &registeredAt
	}
	if json.Unmarshal([]byte(row.PreviousPaths), &record.PreviousPaths) != nil {
		record.PreviousPaths = nil
	}
	return record
}

func (s *Store) RemoveSessionArtifact(ctx context.Context, id domain.SessionID, path string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionArtifact(ctx, gen.DeleteSessionArtifactParams{SessionID: id, Path: path}); err != nil {
		return fmt.Errorf("remove session artifact for %s: %w", id, err)
	}
	return nil
}

func (s *Store) ClearSessionArtifacts(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionArtifacts(ctx, id); err != nil {
		return fmt.Errorf("clear session artifacts for %s: %w", id, err)
	}
	return nil
}
