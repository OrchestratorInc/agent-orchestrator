package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// UseRepositorySandboxSnapshot returns the prepared snapshot a repository's
// sessions on provider/harness boot from, and marks it used. ok is false when
// none has been built yet. Marking it in the same statement makes a lookup and
// the idle collector exclusive: either the lookup refreshes last_used_at first
// and the collector's re-checked condition no longer matches, or the collector
// deletes the row first and the lookup finds nothing.
func (s *Store) UseRepositorySandboxSnapshot(
	ctx context.Context,
	orgID, repositoryKey, provider, harness string,
) (snapshot domain.RepositorySandboxSnapshot, ok bool, err error) {
	err = s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE ao_repository_sandbox_snapshots
			SET last_used_at = now()
			WHERE org_id = $1 AND repository_key = $2 AND provider = $3 AND harness = $4
			RETURNING org_id, repository_key, repository_identity, provider, harness,
				snapshot_id, base_snapshot_id, created_at`,
			orgID, repositoryKey, provider, harness)
		return scanRepositorySnapshot(row, &snapshot, &ok)
	})
	return snapshot, ok, err
}

// RepositorySandboxSnapshot returns the recorded snapshot without marking it
// used, for deciding whether a rebuild is due.
func (s *Store) RepositorySandboxSnapshot(
	ctx context.Context,
	orgID, repositoryKey, provider, harness string,
) (snapshot domain.RepositorySandboxSnapshot, ok bool, err error) {
	err = s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT org_id, repository_key, repository_identity, provider, harness,
				snapshot_id, base_snapshot_id, created_at
			FROM ao_repository_sandbox_snapshots
			WHERE org_id = $1 AND repository_key = $2 AND provider = $3 AND harness = $4`,
			orgID, repositoryKey, provider, harness)
		return scanRepositorySnapshot(row, &snapshot, &ok)
	})
	return snapshot, ok, err
}

func scanRepositorySnapshot(row pgx.Row, snapshot *domain.RepositorySandboxSnapshot, ok *bool) error {
	err := row.Scan(
		&snapshot.OrgID, &snapshot.RepositoryKey, &snapshot.RepositoryIdentity,
		&snapshot.Provider, &snapshot.Harness,
		&snapshot.SnapshotID, &snapshot.BaseSnapshotID, &snapshot.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	*ok = true
	return nil
}

// ReplaceRepositorySandboxSnapshot records a newly built snapshot and returns
// the one it replaced (empty when there was none), so the caller can delete it
// at the provider. Sessions already running are unaffected: a VM does not
// depend on the snapshot it booted from.
func (s *Store) ReplaceRepositorySandboxSnapshot(
	ctx context.Context,
	snapshot domain.RepositorySandboxSnapshot,
) (previousSnapshotID string, err error) {
	err = s.withOrg(ctx, snapshot.OrgID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT snapshot_id FROM ao_repository_sandbox_snapshots
			WHERE org_id = $1 AND repository_key = $2 AND provider = $3 AND harness = $4
			FOR UPDATE`,
			snapshot.OrgID, snapshot.RepositoryKey, snapshot.Provider, snapshot.Harness)
		if scanErr := row.Scan(&previousSnapshotID); scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
			return scanErr
		}
		_, execErr := tx.Exec(ctx, `INSERT INTO ao_repository_sandbox_snapshots
				(org_id, repository_key, provider, harness, repository_identity,
				 snapshot_id, base_snapshot_id, created_at, last_used_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
			ON CONFLICT (org_id, repository_key, provider, harness) DO UPDATE
			SET repository_identity = EXCLUDED.repository_identity,
				snapshot_id = EXCLUDED.snapshot_id,
				base_snapshot_id = EXCLUDED.base_snapshot_id,
				created_at = EXCLUDED.created_at,
				last_used_at = EXCLUDED.last_used_at`,
			snapshot.OrgID, snapshot.RepositoryKey, snapshot.Provider, snapshot.Harness,
			snapshot.RepositoryIdentity, snapshot.SnapshotID, snapshot.BaseSnapshotID)
		return execErr
	})
	if previousSnapshotID == snapshot.SnapshotID {
		previousSnapshotID = ""
	}
	return previousSnapshotID, err
}

// DeleteIdleRepositorySandboxSnapshots removes the provider's snapshots no
// session has booted from since before cutoff, across organizations, and
// returns their provider ids for deletion there. Each row is returned to
// exactly one caller, so collectors on several replicas never double-delete.
func (s *Store) DeleteIdleRepositorySandboxSnapshots(
	ctx context.Context,
	provider string,
	cutoff time.Time,
) ([]string, error) {
	var snapshotIDs []string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `DELETE FROM ao_repository_sandbox_snapshots
			WHERE provider = $1 AND last_used_at < $2
			RETURNING snapshot_id`,
			provider, cutoff)
		if err != nil {
			return err
		}
		snapshotIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return snapshotIDs, err
}
