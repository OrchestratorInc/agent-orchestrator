package store

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListLatestSessionArtifactVersions returns the newest version of each
// artifact path of a session, ordered by path.
func (s *Store) ListLatestSessionArtifactVersions(ctx context.Context, id domain.SessionID) ([]domain.SessionArtifactVersion, error) {
	rows, err := s.qr.ListLatestSessionArtifactVersions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list latest artifact versions for %s: %w", id, err)
	}
	return sessionArtifactVersionsFromGen(rows), nil
}

// ListSessionArtifactVersions returns every version of one artifact path,
// newest first.
func (s *Store) ListSessionArtifactVersions(ctx context.Context, id domain.SessionID, path string) ([]domain.SessionArtifactVersion, error) {
	rows, err := s.qr.ListSessionArtifactVersions(ctx, gen.ListSessionArtifactVersionsParams{SessionID: id, Path: path})
	if err != nil {
		return nil, fmt.Errorf("list artifact versions for %s %q: %w", id, path, err)
	}
	return sessionArtifactVersionsFromGen(rows), nil
}

// AppendSessionArtifactVersion inserts v as the next version of its path,
// ignoring v.Version, and then removes the oldest versions of that path so at
// most keep remain (keep <= 0 keeps all). It returns the inserted version and
// the hashes of the removed rows.
func (s *Store) AppendSessionArtifactVersion(ctx context.Context, v domain.SessionArtifactVersion, keep int) (domain.SessionArtifactVersion, []string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var (
		row    gen.SessionArtifactVersion
		pruned []string
	)
	err := s.inTx(ctx, "append artifact version", func(q *gen.Queries) error {
		next, err := q.NextSessionArtifactVersion(ctx, gen.NextSessionArtifactVersionParams{SessionID: v.SessionID, Path: v.Path})
		if err != nil {
			return err
		}
		row, err = q.InsertSessionArtifactVersion(ctx, gen.InsertSessionArtifactVersionParams{
			SessionID: v.SessionID, Path: v.Path, Version: next, Sha256: v.SHA256, Size: v.Size,
			ContentType: v.ContentType, Mtime: v.ModTime.UnixNano(), CreatedAt: v.CreatedAt,
			Source: string(cmp.Or(v.Source, domain.ArtifactVersionSourceAgent)), Harness: string(v.Harness), RestoredFrom: v.RestoredFrom,
		})
		if err != nil {
			return err
		}
		if keep <= 0 || next <= int64(keep) {
			return nil
		}
		pruned, err = q.DeleteSessionArtifactVersionsBelow(ctx, gen.DeleteSessionArtifactVersionsBelowParams{
			SessionID: v.SessionID, Path: v.Path, MinVersion: next - int64(keep) + 1,
		})
		return err
	})
	if err != nil {
		return domain.SessionArtifactVersion{}, nil, fmt.Errorf("%s %q: %w", v.SessionID, v.Path, err)
	}
	return sessionArtifactVersionFromGen(row), pruned, nil
}

// DeleteSessionArtifactVersions removes every version row of a session, for
// when the user deletes the session's artifacts.
func (s *Store) DeleteSessionArtifactVersions(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteSessionArtifactVersions(ctx, id); err != nil {
		return fmt.Errorf("delete artifact versions for %s: %w", id, err)
	}
	return nil
}

// DeleteSessionArtifactPathVersions removes every version row of one artifact
// path, for when the user deletes that file, and returns the removed rows'
// hashes.
func (s *Store) DeleteSessionArtifactPathVersions(ctx context.Context, id domain.SessionID, path string) ([]string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	sums, err := s.qw.DeleteSessionArtifactPathVersions(ctx, gen.DeleteSessionArtifactPathVersionsParams{SessionID: id, Path: path})
	if err != nil {
		return nil, fmt.Errorf("delete artifact versions for %s %q: %w", id, path, err)
	}
	return sums, nil
}

// CountSessionArtifactVersionsBySHA counts a session's version rows, across
// all paths, whose content is the blob named by sha.
func (s *Store) CountSessionArtifactVersionsBySHA(ctx context.Context, id domain.SessionID, sha string) (int64, error) {
	n, err := s.qr.CountSessionArtifactVersionsBySHA(ctx, gen.CountSessionArtifactVersionsBySHAParams{SessionID: id, Sha256: sha})
	if err != nil {
		return 0, fmt.Errorf("count artifact versions for %s by sha: %w", id, err)
	}
	return n, nil
}

func sessionArtifactVersionsFromGen(rows []gen.SessionArtifactVersion) []domain.SessionArtifactVersion {
	out := make([]domain.SessionArtifactVersion, len(rows))
	for i, row := range rows {
		out[i] = sessionArtifactVersionFromGen(row)
	}
	return out
}

func sessionArtifactVersionFromGen(r gen.SessionArtifactVersion) domain.SessionArtifactVersion {
	return domain.SessionArtifactVersion{
		SessionID: r.SessionID, Path: r.Path, Version: r.Version, SHA256: r.Sha256, Size: r.Size,
		ContentType: r.ContentType, ModTime: time.Unix(0, r.Mtime).UTC(), CreatedAt: r.CreatedAt,
		Source: domain.ArtifactVersionSource(r.Source), Harness: domain.AgentHarness(r.Harness), RestoredFrom: r.RestoredFrom,
	}
}
