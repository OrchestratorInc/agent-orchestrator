package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AddSessionArtifacts registers paths as the session's deliverables and returns
// all of its registered paths, oldest first. Registering a path again changes
// nothing. It returns false for an unknown session.
func (s *Store) AddSessionArtifacts(ctx context.Context, id domain.SessionID, paths []string, registeredAt time.Time) ([]string, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var registered []string
	found := true
	err := s.inTx(ctx, "add session artifacts", func(q *gen.Queries) error {
		var err error
		if found, err = q.SessionIDExists(ctx, id); err != nil || !found {
			return err
		}
		for _, path := range paths {
			if err := q.InsertSessionArtifact(ctx, gen.InsertSessionArtifactParams{
				SessionID: id, Path: path, RegisteredAt: registeredAt,
			}); err != nil {
				return err
			}
		}
		registered, err = q.ListSessionArtifactPaths(ctx, id)
		return err
	})
	if err != nil {
		return nil, false, fmt.Errorf("add session artifacts for %s: %w", id, err)
	}
	return registered, found, nil
}

// ListSessionArtifactPathsForSessions returns the registered paths of each of
// the given sessions, oldest first, keyed by session. A path may name a file
// that no longer exists; reads ignore it until the path is written again.
func (s *Store) ListSessionArtifactPathsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]string, error) {
	out := make(map[domain.SessionID][]string)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListSessionArtifactPathsForSessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list session artifacts: %w", err)
	}
	for _, row := range rows {
		out[row.SessionID] = append(out[row.SessionID], row.Path)
	}
	return out, nil
}

// RemoveSessionArtifact forgets one registered path, if present.
func (s *Store) RemoveSessionArtifact(ctx context.Context, id domain.SessionID, path string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionArtifact(ctx, gen.DeleteSessionArtifactParams{SessionID: id, Path: path}); err != nil {
		return fmt.Errorf("remove session artifact for %s: %w", id, err)
	}
	return nil
}

// ClearSessionArtifacts forgets every registered path of the session.
func (s *Store) ClearSessionArtifacts(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionArtifacts(ctx, id); err != nil {
		return fmt.Errorf("clear session artifacts for %s: %w", id, err)
	}
	return nil
}
