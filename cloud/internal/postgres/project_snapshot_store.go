package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// ProjectSandboxSnapshot returns the prepared snapshot a project's sessions on
// provider/harness boot from. ok is false when none has been built yet.
func (s *Store) ProjectSandboxSnapshot(
	ctx context.Context,
	orgID, projectID, provider, harness string,
) (snapshot domain.ProjectSandboxSnapshot, ok bool, err error) {
	err = s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT org_id, project_id, provider, harness, snapshot_id, base_snapshot_id, created_at
			FROM ao_project_sandbox_snapshots
			WHERE org_id = $1 AND project_id = $2 AND provider = $3 AND harness = $4`,
			orgID, projectID, provider, harness)
		scanErr := row.Scan(
			&snapshot.OrgID, &snapshot.ProjectID, &snapshot.Provider, &snapshot.Harness,
			&snapshot.SnapshotID, &snapshot.BaseSnapshotID, &snapshot.CreatedAt,
		)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		ok = true
		return nil
	})
	return snapshot, ok, err
}

// ReplaceProjectSandboxSnapshot records a newly built snapshot and returns the
// one it replaced (empty when there was none), so the caller can delete it at
// the provider. Sessions already running are unaffected: a VM does not depend
// on the snapshot it booted from.
func (s *Store) ReplaceProjectSandboxSnapshot(
	ctx context.Context,
	snapshot domain.ProjectSandboxSnapshot,
) (previousSnapshotID string, err error) {
	err = s.withOrg(ctx, snapshot.OrgID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT snapshot_id FROM ao_project_sandbox_snapshots
			WHERE org_id = $1 AND project_id = $2 AND provider = $3 AND harness = $4
			FOR UPDATE`,
			snapshot.OrgID, snapshot.ProjectID, snapshot.Provider, snapshot.Harness)
		if scanErr := row.Scan(&previousSnapshotID); scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
			return scanErr
		}
		_, execErr := tx.Exec(ctx, `INSERT INTO ao_project_sandbox_snapshots
				(org_id, project_id, provider, harness, snapshot_id, base_snapshot_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())
			ON CONFLICT (org_id, project_id, provider, harness) DO UPDATE
			SET snapshot_id = EXCLUDED.snapshot_id,
				base_snapshot_id = EXCLUDED.base_snapshot_id,
				created_at = EXCLUDED.created_at`,
			snapshot.OrgID, snapshot.ProjectID, snapshot.Provider, snapshot.Harness,
			snapshot.SnapshotID, snapshot.BaseSnapshotID)
		return execErr
	})
	if previousSnapshotID == snapshot.SnapshotID {
		previousSnapshotID = ""
	}
	return previousSnapshotID, err
}
