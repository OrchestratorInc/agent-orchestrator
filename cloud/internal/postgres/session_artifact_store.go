package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ArtifactChangedEventType is the session event appended when a commit
// changes a session's artifact list.
const ArtifactChangedEventType = "artifact.changed"

const sessionArtifactColumns = `path, name, kind, content_type, size, sha256,
	s3_key, version_id, mtime, updated_at`

// ChangedSessionArtifacts returns the manifest paths whose size or SHA-256
// differ from the session's committed rows, including paths with no row.
func (s *Store) ChangedSessionArtifacts(
	ctx context.Context,
	orgID, sessionID string,
	manifest []domain.ArtifactManifestEntry,
) ([]string, error) {
	changed := make([]string, 0, len(manifest))
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, false); err != nil {
			return err
		}
		rows, err := tx.Query(
			ctx,
			`SELECT path, size, sha256 FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2`,
			orgID, sessionID,
		)
		if err != nil {
			return fmt.Errorf("list committed artifacts: %w", err)
		}
		defer rows.Close()
		type stored struct {
			size   int64
			sha256 string
		}
		committed := map[string]stored{}
		for rows.Next() {
			var path string
			var row stored
			if err := rows.Scan(&path, &row.size, &row.sha256); err != nil {
				return err
			}
			committed[path] = row
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, entry := range manifest {
			row, ok := committed[entry.Path]
			if !ok || row.size != entry.Size || row.sha256 != entry.SHA256 {
				changed = append(changed, entry.Path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// CommitSessionArtifacts upserts and deletes a session's artifact rows,
// recomputes its output type, and appends an artifact.changed event, all in
// one transaction. It returns ErrArtifactLimit, and changes nothing, when the
// result would exceed the session's file count or total size.
func (s *Store) CommitSessionArtifacts(
	ctx context.Context,
	orgID, sessionID string,
	upserts []domain.SessionArtifact,
	deletes []string,
) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, true); err != nil {
			return err
		}
		if len(deletes) > 0 {
			if _, err := tx.Exec(
				ctx,
				`DELETE FROM ao_session_artifacts
				WHERE org_id = $1 AND session_id = $2 AND path = ANY($3)`,
				orgID, sessionID, deletes,
			); err != nil {
				return fmt.Errorf("delete session artifacts: %w", err)
			}
		}
		for _, artifact := range upserts {
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO ao_session_artifacts (
					org_id, session_id, path, name, kind, content_type, size,
					sha256, s3_key, version_id, mtime, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
				ON CONFLICT (session_id, path) DO UPDATE
				SET name = EXCLUDED.name,
					kind = EXCLUDED.kind,
					content_type = EXCLUDED.content_type,
					size = EXCLUDED.size,
					sha256 = EXCLUDED.sha256,
					s3_key = EXCLUDED.s3_key,
					version_id = EXCLUDED.version_id,
					mtime = EXCLUDED.mtime,
					updated_at = now()`,
				orgID, sessionID, artifact.Path, artifact.Name, artifact.Kind,
				artifact.ContentType, artifact.Size, artifact.SHA256,
				artifact.S3Key, artifact.VersionID, artifact.MTime,
			); err != nil {
				return fmt.Errorf("upsert session artifact: %w", normalizeConstraintError(err))
			}
		}
		var files, bytes int64
		var hasPR bool
		if err := tx.QueryRow(
			ctx,
			`SELECT count(*), COALESCE(sum(size), 0)::bigint,
				EXISTS (
					SELECT 1 FROM ao_pull_requests
					WHERE org_id = $1 AND session_id = $2
				)
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2`,
			orgID, sessionID,
		).Scan(&files, &bytes, &hasPR); err != nil {
			return fmt.Errorf("total session artifacts: %w", err)
		}
		if files > domain.MaxSessionArtifactFiles || bytes > domain.MaxSessionArtifactBytes {
			return ErrArtifactLimit
		}
		if _, err := tx.Exec(
			ctx,
			`UPDATE ao_sessions SET output_type = $3 WHERE org_id = $1 AND id = $2`,
			orgID, sessionID, domain.DeriveSessionOutputType(hasPR, files > 0),
		); err != nil {
			return fmt.Errorf("update session output type: %w", err)
		}
		paths := make([]string, 0, len(upserts))
		for _, artifact := range upserts {
			paths = append(paths, artifact.Path)
		}
		payload, err := json.Marshal(map[string][]string{
			"changed": paths,
			"deleted": nonNilPaths(deletes),
		})
		if err != nil {
			return err
		}
		_, err = appendSessionEventTx(ctx, tx, orgID, sessionID, ArtifactChangedEventType, payload)
		return err
	})
}

// ListSessionArtifacts returns a session's committed artifacts sorted by path
// for a member of its organization. A session outside the organization is
// ErrNotFound.
func (s *Store) ListSessionArtifacts(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
) ([]domain.SessionArtifact, error) {
	var artifacts []domain.SessionArtifact
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, false); err != nil {
			return err
		}
		rows, err := tx.Query(
			ctx,
			`SELECT `+sessionArtifactColumns+`
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2
			ORDER BY path`,
			orgID, sessionID,
		)
		if err != nil {
			return fmt.Errorf("list session artifacts: %w", err)
		}
		artifacts, err = pgx.CollectRows(rows, scanSessionArtifact)
		return err
	})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}

// SessionArtifactsBySessions returns the committed artifacts of each listed
// session in one organization, keyed by session ID and sorted by path.
func (s *Store) SessionArtifactsBySessions(
	ctx context.Context,
	orgID string,
	sessionIDs []string,
) (map[string][]domain.SessionArtifact, error) {
	bySession := make(map[string][]domain.SessionArtifact, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return bySession, nil
	}
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`SELECT session_id::text, `+sessionArtifactColumns+`
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = ANY($2::uuid[])
			ORDER BY session_id, path`,
			orgID, sessionIDs,
		)
		if err != nil {
			return fmt.Errorf("list session artifacts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID string
			var artifact domain.SessionArtifact
			if err := rows.Scan(
				&sessionID, &artifact.Path, &artifact.Name, &artifact.Kind,
				&artifact.ContentType, &artifact.Size, &artifact.SHA256,
				&artifact.S3Key, &artifact.VersionID, &artifact.MTime, &artifact.UpdatedAt,
			); err != nil {
				return err
			}
			bySession[sessionID] = append(bySession[sessionID], artifact)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return bySession, nil
}

// SessionArtifact returns one committed artifact row, or ErrNotFound.
func (s *Store) SessionArtifact(
	ctx context.Context,
	orgID, sessionID, path string,
) (domain.SessionArtifact, error) {
	var artifact domain.SessionArtifact
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`SELECT `+sessionArtifactColumns+`
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2 AND path = $3`,
			orgID, sessionID, path,
		)
		if err != nil {
			return fmt.Errorf("get session artifact: %w", err)
		}
		artifact, err = pgx.CollectExactlyOneRow(rows, scanSessionArtifact)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return artifact, err
}

func scanSessionArtifact(row pgx.CollectableRow) (domain.SessionArtifact, error) {
	var artifact domain.SessionArtifact
	err := row.Scan(
		&artifact.Path, &artifact.Name, &artifact.Kind, &artifact.ContentType,
		&artifact.Size, &artifact.SHA256, &artifact.S3Key, &artifact.VersionID,
		&artifact.MTime, &artifact.UpdatedAt,
	)
	return artifact, err
}

// requireSessionTx returns ErrNotFound unless the session exists in the
// organization. lock takes the session row lock, which serializes commits.
func requireSessionTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, lock bool) error {
	query := `SELECT 1 FROM ao_sessions WHERE org_id = $1 AND id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	var found int
	err := tx.QueryRow(ctx, query, orgID, sessionID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func nonNilPaths(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}
