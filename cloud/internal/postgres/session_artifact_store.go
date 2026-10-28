package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ArtifactChangedEventType is the session event appended when a commit
// changes a session's artifact list.
const ArtifactChangedEventType = "artifact.changed"

const sessionArtifactColumns = `path, name, kind, content_type, size, sha256,
	s3_key, version_id, mtime, updated_at`

// ErrArtifactVersionUnknown reports a commit naming an object version that is
// not an open upload of the session: never recorded, already committed, or
// expired and claimed by the sweeper.
var ErrArtifactVersionUnknown = errors.New("artifact version is not an open upload")

// ArtifactUploadAllowed reports whether the session can take one more
// upload of size bytes while its queued versions (uncommitted uploads and
// replaced versions not yet swept) stay within their limits. RecordArtifactUpload enforces the same
// limit exactly; this check only avoids writing an object that would be
// refused.
func (s *Store) ArtifactUploadAllowed(ctx context.Context, orgID, sessionID string, size int64) (bool, error) {
	var allowed bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		count, bytes, err := pendingArtifactUploadsTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		allowed = count+1 <= domain.MaxPendingArtifactUploads && bytes+size <= domain.MaxPendingArtifactBytes
		return nil
	})
	return allowed, err
}

// RecordArtifactUpload records an uploaded object version that the session
// has not committed yet. The sweeper deletes it after ArtifactUploadTTL unless
// a commit claims it first. It returns ErrArtifactLimit, and records nothing,
// when the session's uncommitted uploads would exceed their limits.
func (s *Store) RecordArtifactUpload(ctx context.Context, orgID, sessionID, s3Key, versionID string, size int64) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, true); err != nil {
			return err
		}
		count, bytes, err := pendingArtifactUploadsTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		if count+1 > domain.MaxPendingArtifactUploads || bytes+size > domain.MaxPendingArtifactBytes {
			return ErrArtifactLimit
		}
		if _, err := tx.Exec(
			ctx,
			`INSERT INTO ao_artifact_object_versions (
				org_id, session_id, s3_key, version_id, reason, size, collect_after
			) VALUES ($1, $2, $3, $4, 'upload', $5, now() + $6::interval)
			ON CONFLICT (s3_key, version_id) DO NOTHING`,
			orgID, sessionID, s3Key, versionID, size, domain.ArtifactUploadTTL.String(),
		); err != nil {
			return fmt.Errorf("record artifact upload: %w", normalizeConstraintError(err))
		}
		return nil
	})
}

// pendingArtifactUploadsTx counts every version of the session still waiting
// in the bucket: open uploads and the versions commits replaced or deleted, so
// committing in a loop cannot pile up versions faster than the sweeper drains
// them.
func pendingArtifactUploadsTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (int64, int64, error) {
	var count, bytes int64
	err := tx.QueryRow(
		ctx,
		`SELECT count(*), COALESCE(sum(size), 0)::bigint
		FROM ao_artifact_object_versions
		WHERE org_id = $1 AND session_id = $2`,
		orgID, sessionID,
	).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("count pending artifact uploads: %w", err)
	}
	return count, bytes, nil
}

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
// one transaction. Each upserted version must be an open upload of the
// session; versions the commit replaces or deletes are queued for the
// sweeper. It returns ErrStaleWorker when the worker's connection was
// replaced, ErrArtifactVersionUnknown for a version that is not an open
// upload, and ErrArtifactLimit when the result would exceed the session's
// file count or total size; each changes nothing. A commit that changes
// nothing appends no event.
func (s *Store) CommitSessionArtifacts(
	ctx context.Context,
	orgID, sessionID, workerID string,
	workerEpoch int64,
	upserts []domain.SessionArtifact,
	deletes []string,
) error {
	if len(upserts) == 0 && len(deletes) == 0 {
		return nil
	}
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, true); err != nil {
			return err
		}
		// A deleted project's sessions take no new artifacts.
		var projectDeleted bool
		if err := tx.QueryRow(
			ctx,
			`SELECT project.archived_at IS NOT NULL
			FROM ao_sessions session
			JOIN ao_projects project
			  ON project.org_id = session.org_id AND project.id = session.project_id
			WHERE session.org_id = $1 AND session.id = $2`,
			orgID, sessionID,
		).Scan(&projectDeleted); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check artifact project: %w", err)
		}
		if projectDeleted {
			return ErrNotFound
		}
		var current bool
		if err := tx.QueryRow(
			ctx,
			`SELECT EXISTS (
				SELECT 1 FROM ao_worker_connections
				WHERE org_id = $1 AND session_id = $2 AND worker_id = $3
					AND epoch = $4 AND disconnected_at IS NULL
			)`,
			orgID, sessionID, workerID, workerEpoch,
		).Scan(&current); err != nil {
			return fmt.Errorf("check artifact worker: %w", err)
		}
		if !current {
			return ErrStaleWorker
		}
		if len(upserts) > 0 {
			keys := make([]string, len(upserts))
			versions := make([]string, len(upserts))
			for index, artifact := range upserts {
				keys[index] = artifact.S3Key
				versions[index] = artifact.VersionID
			}
			claimed, err := tx.Exec(
				ctx,
				`DELETE FROM ao_artifact_object_versions version
				USING unnest($3::text[], $4::text[]) AS committed(s3_key, version_id)
				WHERE version.org_id = $1 AND version.session_id = $2
					AND version.s3_key = committed.s3_key
					AND version.version_id = committed.version_id
					AND version.reason = 'upload'
					AND version.collect_after > now()
					AND version.collecting_until IS NULL`,
				orgID, sessionID, keys, versions,
			)
			if err != nil {
				return fmt.Errorf("claim artifact uploads: %w", err)
			}
			if claimed.RowsAffected() != int64(len(upserts)) {
				return ErrArtifactVersionUnknown
			}
		}
		paths := make([]string, 0, len(upserts)+len(deletes))
		next := make(map[string]string, len(upserts))
		for _, artifact := range upserts {
			paths = append(paths, artifact.Path)
			next[artifact.Path] = artifact.VersionID
		}
		paths = append(paths, deletes...)
		rows, err := tx.Query(
			ctx,
			`SELECT path, s3_key, version_id, size FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2 AND path = ANY($3)`,
			orgID, sessionID, paths,
		)
		if err != nil {
			return fmt.Errorf("read replaced artifacts: %w", err)
		}
		type previous struct {
			path, s3Key, versionID string
			size                   int64
		}
		var existing []previous
		for rows.Next() {
			var row previous
			if err := rows.Scan(&row.path, &row.s3Key, &row.versionID, &row.size); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		deleted := make([]string, 0, len(deletes))
		for _, row := range existing {
			version, upserted := next[row.path]
			if upserted && version == row.versionID {
				continue
			}
			if !upserted {
				deleted = append(deleted, row.path)
			}
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO ao_artifact_object_versions (
					org_id, session_id, s3_key, version_id, reason, size, collect_after
				) VALUES ($1, $2, $3, $4, 'replaced', $5, now() + $6::interval)
				ON CONFLICT (s3_key, version_id) DO NOTHING`,
				orgID, sessionID, row.s3Key, row.versionID, row.size, domain.ArtifactReplacedGrace.String(),
			); err != nil {
				return fmt.Errorf("queue replaced artifact version: %w", err)
			}
		}
		if len(upserts) == 0 && len(deleted) == 0 {
			return nil
		}
		if len(deleted) > 0 {
			if _, err := tx.Exec(
				ctx,
				`DELETE FROM ao_session_artifacts
				WHERE org_id = $1 AND session_id = $2 AND path = ANY($3)`,
				orgID, sessionID, deleted,
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
		totals, err := sessionArtifactTotalsTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		if totals.files > domain.MaxSessionArtifactFiles || totals.bytes > domain.MaxSessionArtifactBytes {
			return ErrArtifactLimit
		}
		changed := make([]string, 0, len(upserts))
		for _, artifact := range upserts {
			changed = append(changed, artifact.Path)
		}
		return recordArtifactChangeTx(ctx, tx, orgID, sessionID, totals, changed, deleted)
	})
}

// ArtifactsChangedNotifyPrefix marks an ao_worker_work notification that
// tells the session's worker its committed artifacts changed without it.
const ArtifactsChangedNotifyPrefix = "artifacts:"

// DeleteSessionArtifact deletes one committed artifact of a session for a
// member of its organization, and returns the object version it pointed to.
// In one transaction it removes the row, queues the version for collection
// now, updates the session's output type, appends an artifact.changed event,
// and notifies the session's worker, which removes the file from its disk
// unless the file changed there since. A path with no committed file, or a
// session outside the organization, is ErrNotFound.
func (s *Store) DeleteSessionArtifact(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID, path string,
) (domain.ArtifactObjectVersion, error) {
	var version domain.ArtifactObjectVersion
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		if err := requireSessionTx(ctx, tx, orgID, sessionID, true); err != nil {
			return err
		}
		var size int64
		err := tx.QueryRow(
			ctx,
			`DELETE FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2 AND path = $3
			RETURNING s3_key, version_id, size`,
			orgID, sessionID, path,
		).Scan(&version.S3Key, &version.VersionID, &size)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("delete session artifact: %w", err)
		}
		if _, err := tx.Exec(
			ctx,
			`INSERT INTO ao_artifact_object_versions (
				org_id, session_id, s3_key, version_id, reason, size, collect_after
			) VALUES ($1, $2, $3, $4, 'replaced', $5, now())
			ON CONFLICT (s3_key, version_id) DO UPDATE SET collect_after = now()`,
			orgID, sessionID, version.S3Key, version.VersionID, size,
		); err != nil {
			return fmt.Errorf("queue deleted artifact version: %w", err)
		}
		totals, err := sessionArtifactTotalsTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		if err := recordArtifactChangeTx(ctx, tx, orgID, sessionID, totals, []string{}, []string{path}); err != nil {
			return err
		}
		// Delivered on commit. A worker that is not connected removes the
		// file when it next starts.
		if _, err := tx.Exec(ctx, `SELECT pg_notify('ao_worker_work', $1)`, ArtifactsChangedNotifyPrefix+sessionID); err != nil {
			return fmt.Errorf("notify artifact change: %w", err)
		}
		return nil
	})
	return version, err
}

// deleteProjectArtifactsTx removes the committed artifacts of every session in
// a project and queues all of their object versions, committed, replaced or
// still uploading, for the sweeper to delete now. The project's sessions keep
// their pull requests in their output type.
func deleteProjectArtifactsTx(ctx context.Context, tx pgx.Tx, orgID, projectID string) error {
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO ao_artifact_object_versions (
			org_id, session_id, s3_key, version_id, reason, size, collect_after
		)
		SELECT artifact.org_id, artifact.session_id, artifact.s3_key,
			artifact.version_id, 'replaced', artifact.size, now()
		FROM ao_session_artifacts artifact
		JOIN ao_sessions session
		  ON session.org_id = artifact.org_id AND session.id = artifact.session_id
		WHERE session.org_id = $1 AND session.project_id = $2
		ON CONFLICT (s3_key, version_id) DO UPDATE SET collect_after = now()`,
		orgID, projectID,
	); err != nil {
		return fmt.Errorf("queue project artifact versions: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE ao_artifact_object_versions version
		SET collect_after = now()
		FROM ao_sessions session
		WHERE session.org_id = $1 AND session.project_id = $2
		  AND version.org_id = session.org_id AND version.session_id = session.id
		  AND version.collect_after > now()
		  AND version.collecting_until IS NULL`,
		orgID, projectID,
	); err != nil {
		return fmt.Errorf("collect project artifact versions now: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`DELETE FROM ao_session_artifacts artifact
		USING ao_sessions session
		WHERE session.org_id = $1 AND session.project_id = $2
		  AND artifact.org_id = session.org_id AND artifact.session_id = session.id`,
		orgID, projectID,
	); err != nil {
		return fmt.Errorf("delete project artifacts: %w", err)
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE ao_sessions session
		SET output_type = CASE WHEN EXISTS (
			SELECT 1 FROM ao_pull_requests pr
			WHERE pr.org_id = session.org_id AND pr.session_id = session.id
		) THEN $3 ELSE $4 END
		WHERE session.org_id = $1 AND session.project_id = $2
		  AND session.output_type IN ($5, $6)`,
		orgID, projectID, domain.SessionOutputPR, domain.SessionOutputNone,
		domain.SessionOutputArtifact, domain.SessionOutputPRAndArtifact,
	); err != nil {
		return fmt.Errorf("update project session output types: %w", err)
	}
	return nil
}

type sessionArtifactTotals struct {
	files, bytes int64
	hasPR        bool
}

// sessionArtifactTotalsTx counts a session's committed artifacts and their
// bytes, and reports whether it owns a pull request.
func sessionArtifactTotalsTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (sessionArtifactTotals, error) {
	var totals sessionArtifactTotals
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
	).Scan(&totals.files, &totals.bytes, &totals.hasPR); err != nil {
		return totals, fmt.Errorf("total session artifacts: %w", err)
	}
	return totals, nil
}

// recordArtifactChangeTx sets the session's output type from its totals and
// appends an artifact.changed event naming the changed and deleted paths.
func recordArtifactChangeTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, totals sessionArtifactTotals, changed, deleted []string) error {
	if _, err := tx.Exec(
		ctx,
		`UPDATE ao_sessions SET output_type = $3 WHERE org_id = $1 AND id = $2`,
		orgID, sessionID, domain.DeriveSessionOutputType(totals.hasPR, totals.files > 0),
	); err != nil {
		return fmt.Errorf("update session output type: %w", err)
	}
	payload, err := json.Marshal(map[string][]string{
		"changed": changed,
		"deleted": deleted,
	})
	if err != nil {
		return err
	}
	_, err = appendSessionEventTx(ctx, tx, orgID, sessionID, ArtifactChangedEventType, payload)
	return err
}

// ClaimArtifactObjectVersions claims up to limit object versions whose
// collect_after has passed, for lease, and returns them. A claimed version
// can no longer be committed. A claim that is not followed by
// ForgetArtifactObjectVersions lapses after lease and is claimed again.
func (s *Store) ClaimArtifactObjectVersions(ctx context.Context, limit int, lease time.Duration) ([]domain.ArtifactObjectVersion, error) {
	var versions []domain.ArtifactObjectVersion
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`UPDATE ao_artifact_object_versions version
			SET collecting_until = now() + $2::interval
			FROM (
				SELECT s3_key, version_id FROM ao_artifact_object_versions
				WHERE collect_after <= now()
					AND (collecting_until IS NULL OR collecting_until <= now())
				ORDER BY collect_after
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			) due
			WHERE version.s3_key = due.s3_key AND version.version_id = due.version_id
			RETURNING version.s3_key, version.version_id`,
			limit, lease.String(),
		)
		if err != nil {
			return fmt.Errorf("claim artifact object versions: %w", err)
		}
		versions, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ArtifactObjectVersion, error) {
			var version domain.ArtifactObjectVersion
			err := row.Scan(&version.S3Key, &version.VersionID)
			return version, err
		})
		return err
	})
	return versions, err
}

// ForgetArtifactObjectVersions removes collected object versions from the
// sweeper's queue.
func (s *Store) ForgetArtifactObjectVersions(ctx context.Context, versions []domain.ArtifactObjectVersion) error {
	if len(versions) == 0 {
		return nil
	}
	keys := make([]string, len(versions))
	ids := make([]string, len(versions))
	for index, version := range versions {
		keys[index] = version.S3Key
		ids[index] = version.VersionID
	}
	return s.withService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(
			ctx,
			`DELETE FROM ao_artifact_object_versions version
			USING unnest($1::text[], $2::text[]) AS collected(s3_key, version_id)
			WHERE version.s3_key = collected.s3_key AND version.version_id = collected.version_id`,
			keys, ids,
		); err != nil {
			return fmt.Errorf("forget artifact object versions: %w", err)
		}
		return nil
	})
}

// RequireSessionArtifactAccess returns ErrNotFound unless the session exists
// in the organization, for a member of it.
func (s *Store) RequireSessionArtifactAccess(ctx context.Context, principal domain.Principal, orgID, sessionID string) error {
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		return requireSessionTx(ctx, tx, orgID, sessionID, false)
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

// SessionArtifactCounts returns the committed artifact count and newest
// commit time of each listed session in one organization that has any.
func (s *Store) SessionArtifactCounts(
	ctx context.Context,
	orgID string,
	sessionIDs []string,
) (map[string]domain.SessionArtifactSummary, error) {
	counts := make(map[string]domain.SessionArtifactSummary, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return counts, nil
	}
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`SELECT session_id::text, count(*), max(updated_at)
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = ANY($2::uuid[])
			GROUP BY session_id`,
			orgID, sessionIDs,
		)
		if err != nil {
			return fmt.Errorf("count session artifacts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID string
			var summary domain.SessionArtifactSummary
			if err := rows.Scan(&sessionID, &summary.Count, &summary.UpdatedAt); err != nil {
				return err
			}
			counts[sessionID] = summary
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// SessionArtifact returns one committed artifact row for userID, or
// ErrNotFound. It also returns ErrNotFound unless userID is an active member
// of the organization and the organization is active, so an access link stops
// working when its user leaves.
func (s *Store) SessionArtifact(
	ctx context.Context,
	userID, orgID, sessionID, path string,
) (domain.SessionArtifact, error) {
	var artifact domain.SessionArtifact
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`SELECT `+sessionArtifactColumns+`
			FROM ao_session_artifacts
			WHERE org_id = $1 AND session_id = $2 AND path = $3
				AND EXISTS (
					SELECT 1
					FROM ao_org_memberships membership
					JOIN ao_organizations organization ON organization.id = membership.org_id
					WHERE membership.org_id = $1
						AND membership.user_id = $4
						AND membership.status = 'active'
						AND organization.status = 'active'
				)`,
			orgID, sessionID, path, userID,
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
// organization. lock takes the session row lock, which serializes artifact
// commits and uploads; FOR NO KEY UPDATE leaves inserts into tables that
// reference the session unblocked.
func requireSessionTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, lock bool) error {
	query := `SELECT 1 FROM ao_sessions WHERE org_id = $1 AND id = $2`
	if lock {
		query += ` FOR NO KEY UPDATE`
	}
	var found int
	err := tx.QueryRow(ctx, query, orgID, sessionID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
