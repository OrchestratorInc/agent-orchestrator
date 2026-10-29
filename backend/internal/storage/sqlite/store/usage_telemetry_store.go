package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UsageMessageBatch contains only a count and local routing identifiers.
type UsageMessageBatch struct {
	SessionID          string
	ProjectID          string
	ProjectTelemetryID string
	Count              int64
	FirstAt            time.Time
	LastAt             time.Time
}

// UsagePRFact is one locally deduplicated provider PR association.
type UsagePRFact struct {
	Key                string
	ProjectID          string
	Merged             bool
	Historical         bool
	HistoricalMerged   bool
	AssociationPending bool
	MergePending       bool
}

// UsageProjectID is random for each project and install. Neither project
// slug nor repository data contributes to the exported ID.
func (s *Store) UsageProjectID(ctx context.Context, projectID, installID string) (string, error) {
	if projectID == "" || installID == "" {
		return "", fmt.Errorf("usage identity requires project and install")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_project_identity
		(project_id, install_id, telemetry_id) VALUES (?, ?, ?)`, projectID, installID, uuid.NewString())
	if err != nil {
		return "", err
	}
	var id string
	err = s.writeDB.QueryRowContext(ctx, `SELECT telemetry_id FROM usage_project_identity
		WHERE project_id = ? AND install_id = ?`, projectID, installID).Scan(&id)
	return id, err
}

// DrainUsageMessages atomically reserves at most limit session summaries.
// The retained row is bounded even if the daemon restarts before a flush.
func (s *Store) DrainUsageMessages(ctx context.Context, limit int, terminatedOnly bool, installID string) ([]UsageMessageBatch, error) {
	if installID == "" {
		return nil, fmt.Errorf("usage identity requires install")
	}
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := func() ([]UsageMessageBatch, error) {
		rows, err := tx.QueryContext(ctx, `SELECT m.session_id, s.project_id, m.pending_count, m.first_at, m.last_at
		FROM usage_message_summary m JOIN sessions s ON s.id = m.session_id
		WHERE m.pending_count > 0 AND s.project_id IS NOT NULL
			AND (? = 0 OR s.is_terminated = 1)
		ORDER BY m.session_id LIMIT ?`, terminatedOnly, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var batches []UsageMessageBatch
		for rows.Next() {
			var b UsageMessageBatch
			var first, last sql.NullTime
			if err := rows.Scan(&b.SessionID, &b.ProjectID, &b.Count, &first, &last); err != nil {
				return nil, err
			}
			b.FirstAt, b.LastAt = first.Time, last.Time
			batches = append(batches, b)
		}
		return batches, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_project_identity
			(project_id, install_id, telemetry_id) VALUES (?, ?, ?)`,
			out[i].ProjectID, installID, uuid.NewString()); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT telemetry_id FROM usage_project_identity
			WHERE project_id = ? AND install_id = ?`, out[i].ProjectID, installID).Scan(&out[i].ProjectTelemetryID); err != nil {
			return nil, err
		}
	}
	for _, b := range out {
		if _, err := tx.ExecContext(ctx, `UPDATE usage_message_summary
			SET pending_count = 0, first_at = NULL, last_at = NULL
			WHERE session_id = ?`, b.SessionID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

// DiscardUsageMessages clears unsent counts when the installation opts out.
func (s *Store) DiscardUsageMessages(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `UPDATE usage_message_summary
		SET pending_count = 0, first_at = NULL, last_at = NULL WHERE pending_count > 0`)
	return err
}

// PendingWorkerPRUsage reads the durable association, not agent creation.
// The PR table has one canonical row per provider identity.
func (s *Store) PendingWorkerPRUsage(ctx context.Context, limit int) ([]UsagePRFact, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.readDB.QueryContext(ctx, `SELECT m.pr_key, m.project_id,
		p.pr_state = 'merged', m.historical, m.historical_merged, m.associated_reported = 0,
		(p.pr_state = 'merged' AND m.merged_reported = 0)
		FROM usage_pr_measurement m
		JOIN pr p ON m.pr_key = CASE WHEN p.provider_id <> '' AND p.provider <> ''
			THEN p.provider || ':' || p.host || ':' || p.provider_id ELSE p.url END
		WHERE (m.associated_reported = 0 OR
			(p.pr_state = 'merged' AND m.merged_reported = 0))
		ORDER BY m.pr_key LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UsagePRFact
	for rows.Next() {
		var p UsagePRFact
		if err := rows.Scan(&p.Key, &p.ProjectID, &p.Merged, &p.Historical, &p.HistoricalMerged,
			&p.AssociationPending, &p.MergePending); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkWorkerPRUsageReported reserves an association and merge outcome once.
func (s *Store) MarkWorkerPRUsageReported(ctx context.Context, key string, association, merge bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `UPDATE usage_pr_measurement SET
		associated_reported = CASE WHEN ? THEN 1 ELSE associated_reported END,
		merged_reported = CASE WHEN ? THEN 1 ELSE merged_reported END
		WHERE pr_key = ?`, association, merge, key)
	return err
}

// DiscardPendingWorkerPRUsage is used while opted out. A later opt-in
// cannot export associations or merges that occurred during the opt-out.
func (s *Store) DiscardPendingWorkerPRUsage(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE usage_pr_measurement
		SET associated_reported = 1 WHERE associated_reported = 0`); err != nil {
		return err
	}
	// Scan merged PRs once, then use the measurement primary key for matching.
	if _, err := tx.ExecContext(ctx, `UPDATE usage_pr_measurement SET merged_reported = 1
		WHERE merged_reported = 0 AND pr_key IN (
			SELECT CASE WHEN provider_id <> '' AND provider <> ''
				THEN provider || ':' || host || ':' || provider_id ELSE url END
			FROM pr WHERE pr_state = 'merged'
		)`); err != nil {
		return err
	}
	return tx.Commit()
}
