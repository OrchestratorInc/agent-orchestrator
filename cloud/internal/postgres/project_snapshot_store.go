package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// retiredRepositorySnapshotKeyPrefix keys a snapshot that a rebuild replaced
// while a live session's plan still named it. The row keeps the provider copy
// tracked (so the collector deletes it once no session needs it) without ever
// being served: lookups use "github:" or "url:" keys only.
const retiredRepositorySnapshotKeyPrefix = "retired:"

// repositorySnapshotReferenced is true when a session that is not being
// deleted still names the snapshot row aliased "snapshot" in its plan. Such a
// session can be recreated from it at any time (Freestyle Recreate boots the
// recorded snapshot), however long ago it started, so the snapshot is in use.
const repositorySnapshotReferenced = `EXISTS (
	SELECT 1 FROM ao_sandboxes sandbox
	WHERE sandbox.org_id = snapshot.org_id
	  AND sandbox.provider = snapshot.provider
	  AND sandbox.desired_state <> 'deleted'
	  AND sandbox.resource_profile -> sandbox.provider ->> 'snapshot' = snapshot.snapshot_id)`

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
// the id of the one it replaced (empty when there was none). The replaced
// snapshot is never deleted here: it is kept under a retired key that lookups
// never match, where the idle collector deletes it at the provider as soon as
// no live session's plan references it (a recreate of such a session boots
// it). Routing it through the collector also retries a failed provider delete
// instead of leaking the copy.
func (s *Store) ReplaceRepositorySandboxSnapshot(
	ctx context.Context,
	snapshot domain.RepositorySandboxSnapshot,
) (previousSnapshotID string, err error) {
	err = s.withOrg(ctx, snapshot.OrgID, func(tx pgx.Tx) error {
		var previousIdentity, previousBase string
		row := tx.QueryRow(ctx, `SELECT snapshot_id, repository_identity, base_snapshot_id
			FROM ao_repository_sandbox_snapshots
			WHERE org_id = $1 AND repository_key = $2 AND provider = $3 AND harness = $4
			FOR UPDATE`,
			snapshot.OrgID, snapshot.RepositoryKey, snapshot.Provider, snapshot.Harness)
		if scanErr := row.Scan(
			&previousSnapshotID, &previousIdentity, &previousBase,
		); scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
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
		if execErr != nil {
			return execErr
		}
		if previousSnapshotID == "" || previousSnapshotID == snapshot.SnapshotID {
			return nil
		}
		// Retired with an ancient last_used_at, so the collector takes it on its
		// next run once no session references it.
		_, execErr = tx.Exec(ctx, `INSERT INTO ao_repository_sandbox_snapshots
				(org_id, repository_key, provider, harness, repository_identity,
				 snapshot_id, base_snapshot_id, created_at, last_used_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), to_timestamp(0))
			ON CONFLICT (org_id, repository_key, provider, harness) DO NOTHING`,
			snapshot.OrgID, retiredRepositorySnapshotKeyPrefix+previousSnapshotID,
			snapshot.Provider, snapshot.Harness, previousIdentity,
			previousSnapshotID, previousBase,
		)
		return execErr
	})
	if err != nil || previousSnapshotID == snapshot.SnapshotID {
		previousSnapshotID = ""
	}
	return previousSnapshotID, err
}

// CollectIdleRepositorySandboxSnapshots deletes the provider's snapshots no
// session has booted from since before cutoff and no live session's plan
// references, across organizations, and returns the ids it deleted. Each
// candidate is locked (SKIP LOCKED, so collectors on several replicas split
// the work and never double-delete) while remove deletes the provider copy;
// the row is deleted only once remove succeeds, so a failed provider delete
// keeps the row and is retried on the next run instead of leaking the copy.
// A lookup racing the collection waits on the row lock and then either finds
// the row (remove failed) or finds nothing.
func (s *Store) CollectIdleRepositorySandboxSnapshots(
	ctx context.Context,
	provider string,
	cutoff time.Time,
	remove func(ctx context.Context, snapshotID string) error,
) (collected []string, err error) {
	failed := []string{}
	var failures []error
	for ctx.Err() == nil {
		var (
			found      bool
			snapshotID string
		)
		err := s.withService(ctx, func(tx pgx.Tx) error {
			var orgID, repositoryKey, harness string
			row := tx.QueryRow(ctx, `SELECT snapshot.org_id::text, snapshot.repository_key,
					snapshot.harness, snapshot.snapshot_id
				FROM ao_repository_sandbox_snapshots snapshot
				WHERE snapshot.provider = $1 AND snapshot.last_used_at < $2
				  AND NOT (snapshot.snapshot_id = ANY($3::text[]))
				  AND NOT `+repositorySnapshotReferenced+`
				ORDER BY snapshot.last_used_at
				LIMIT 1
				FOR UPDATE OF snapshot SKIP LOCKED`,
				provider, cutoff, failed)
			if scanErr := row.Scan(&orgID, &repositoryKey, &harness, &snapshotID); scanErr != nil {
				if errors.Is(scanErr, pgx.ErrNoRows) {
					return nil
				}
				return scanErr
			}
			found = true
			if removeErr := remove(ctx, snapshotID); removeErr != nil {
				failed = append(failed, snapshotID)
				failures = append(failures, fmt.Errorf("delete snapshot %s: %w", snapshotID, removeErr))
				return nil
			}
			if _, execErr := tx.Exec(ctx, `DELETE FROM ao_repository_sandbox_snapshots
				WHERE org_id = $1 AND repository_key = $2 AND provider = $3 AND harness = $4
				  AND snapshot_id = $5`,
				orgID, repositoryKey, provider, harness, snapshotID); execErr != nil {
				return execErr
			}
			collected = append(collected, snapshotID)
			return nil
		})
		if err != nil {
			return collected, err
		}
		if !found {
			break
		}
	}
	return collected, errors.Join(failures...)
}

// AcquireRepositorySnapshotBuild claims the build of one repository snapshot
// across control-plane replicas with a session-level advisory lock held on a
// dedicated connection. ok is false when another replica holds it; otherwise
// the caller must call release once the build is done.
func (s *Store) AcquireRepositorySnapshotBuild(
	ctx context.Context,
	orgID, repositoryKey, provider, harness string,
) (release func(), ok bool, err error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	key := "ao-repository-snapshot-build:" + orgID + "/" + repositoryKey + "/" + provider + "/" + harness
	if err := connection.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key,
	).Scan(&ok); err != nil {
		connection.Release()
		return nil, false, err
	}
	if !ok {
		connection.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := connection.Exec(unlockCtx,
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key); err != nil {
			// A connection whose unlock failed may still hold the lock; closing
			// it ends the session and with it the lock.
			_ = connection.Conn().Close(unlockCtx)
		}
		connection.Release()
	}, true, nil
}
