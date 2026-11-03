package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// UsageMessageBatch contains only counts and local routing identifiers.
type UsageMessageBatch struct {
	SessionID          string
	ProjectID          string
	ProjectTelemetryID string
	// Count is direct human Chat messages, as first measured. SteerCount and
	// SendCount are the other human sends; the surface counts split all three.
	Count        int64
	SteerCount   int64
	SendCount    int64
	DesktopCount int64
	MobileCount  int64
	CLICount     int64
	// Kind and Harness describe the session, for segmenting the counts.
	Kind    string
	Harness string
	FirstAt time.Time
	LastAt  time.Time
}

// usageMessageKind is which human send a usage message count records.
type usageMessageKind int

// Usage message kinds.
const (
	// usageMessageChat is a direct Chat message, counted at its durable insert.
	usageMessageChat usageMessageKind = iota
	// usageMessageSteer is guidance steered into a running turn.
	usageMessageSteer
	// usageMessageSend is a person's `/send`: `ao send` outside an AO session,
	// or a TUI session's composer.
	usageMessageSend
)

// usageMessageIncrement adds one message of kind, attributed to the client
// surface the request carried. A message with no request surface (none today)
// still counts toward its kind.
func usageMessageIncrement(ctx context.Context, session domain.SessionID, kind usageMessageKind, now time.Time) gen.IncrementUsageMessageSummaryParams {
	p := gen.IncrementUsageMessageSummaryParams{
		SessionID: string(session), FirstAt: sql.NullTime{Time: now, Valid: true}, LastAt: sql.NullTime{Time: now, Valid: true},
	}
	switch kind {
	case usageMessageSteer:
		p.Steer = 1
	case usageMessageSend:
		p.Send = 1
	default:
		p.Chat = 1
	}
	if surface, ok := telemetrymeta.SurfaceFromContext(ctx); ok {
		switch surface {
		case domain.RequestSurfaceDesktop:
			p.Desktop = 1
		case domain.RequestSurfaceMobile:
			p.Mobile = 1
		case domain.RequestSurfaceCLI:
			p.Cli = 1
		}
	}
	return p
}

// RecordUsageHumanSend counts a human send the Chat insert does not see: a
// steer, or a `/send`. Callers decide authorship. Nothing is recorded while
// usage telemetry is off, and standalone sessions are excluded like Chat
// messages.
func (s *Store) RecordUsageHumanSend(ctx context.Context, session domain.SessionID, steer bool) error {
	if !s.usageAllowed() {
		return nil
	}
	kind := usageMessageSend
	if steer {
		kind = usageMessageSteer
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.IncrementUsageMessageSummary(ctx, usageMessageIncrement(ctx, session, kind, time.Now().UTC()))
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
	// ClosePending is a PR closed without merging and not yet reported.
	ClosePending bool
	// MergedVia is the client that asked AO to merge; empty when the merge was
	// only observed.
	MergedVia string
	// Detail for new outcomes. Provider is the SCM kind (github, gitlab); the
	// session fields describe the owning worker; times are zero when unknown.
	Provider         string
	SessionKind      string
	Harness          string
	SessionCreatedAt time.Time
	PRCreatedAt      time.Time
	MergedAt         time.Time
	ClosedAt         time.Time
	ReviewRounds     int64
	AOReviewUsed     bool
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
		rows, err := tx.QueryContext(ctx, `SELECT m.session_id, s.project_id, m.pending_count,
			m.steer_count, m.send_count, m.desktop_count, m.mobile_count, m.cli_count,
			s.kind, s.harness, m.first_at, m.last_at
		FROM usage_message_summary m JOIN sessions s ON s.id = m.session_id
		WHERE (m.pending_count > 0 OR m.steer_count > 0 OR m.send_count > 0) AND s.project_id IS NOT NULL
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
			if err := rows.Scan(&b.SessionID, &b.ProjectID, &b.Count, &b.SteerCount, &b.SendCount,
				&b.DesktopCount, &b.MobileCount, &b.CLICount, &b.Kind, &b.Harness, &first, &last); err != nil {
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
			SET pending_count = 0, steer_count = 0, send_count = 0,
			desktop_count = 0, mobile_count = 0, cli_count = 0, first_at = NULL, last_at = NULL
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
		SET pending_count = 0, steer_count = 0, send_count = 0,
			desktop_count = 0, mobile_count = 0, cli_count = 0, first_at = NULL, last_at = NULL WHERE pending_count > 0 OR steer_count > 0 OR send_count > 0`)
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
		(p.pr_state = 'merged' AND m.merged_reported = 0),
		(p.pr_state = 'closed' AND m.closed_reported = 0),
		m.merged_via, p.provider, COALESCE(s.kind, ''), COALESCE(s.harness, ''), s.created_at,
		p.created_at_provider, p.merged_at_provider, p.closed_at_provider,
		(SELECT COUNT(*) FROM pr_reviews r WHERE r.pr_url = p.url),
		EXISTS (SELECT 1 FROM review_run rr WHERE rr.pr_url = p.url)
		FROM usage_pr_measurement m
		JOIN pr p ON m.pr_key = CASE WHEN p.provider_id <> '' AND p.provider <> ''
			THEN p.provider || ':' || p.host || ':' || p.provider_id ELSE p.url END
		LEFT JOIN sessions s ON s.id = p.session_id
		WHERE (m.associated_reported = 0 OR
			(p.pr_state = 'merged' AND m.merged_reported = 0) OR
			(p.pr_state = 'closed' AND m.closed_reported = 0))
		ORDER BY m.pr_key LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UsagePRFact
	for rows.Next() {
		var p UsagePRFact
		var sessionCreated, prCreated, merged, closed sql.NullTime
		if err := rows.Scan(&p.Key, &p.ProjectID, &p.Merged, &p.Historical, &p.HistoricalMerged,
			&p.AssociationPending, &p.MergePending, &p.ClosePending,
			&p.MergedVia, &p.Provider, &p.SessionKind, &p.Harness, &sessionCreated,
			&prCreated, &merged, &closed, &p.ReviewRounds, &p.AOReviewUsed); err != nil {
			return nil, err
		}
		p.SessionCreatedAt, p.PRCreatedAt, p.MergedAt, p.ClosedAt = sessionCreated.Time, prCreated.Time, merged.Time, closed.Time
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkWorkerPRUsageReported reserves an association, merge and close outcome
// once each.
func (s *Store) MarkWorkerPRUsageReported(ctx context.Context, key string, association, merge, closed bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `UPDATE usage_pr_measurement SET
		associated_reported = CASE WHEN ? THEN 1 ELSE associated_reported END,
		merged_reported = CASE WHEN ? THEN 1 ELSE merged_reported END,
		closed_reported = CASE WHEN ? THEN 1 ELSE closed_reported END
		WHERE pr_key = ?`, association, merge, closed, key)
	return err
}

// WorkerPRUsageHistory reports whether any worker PR association, and any
// merge, was already measured (reported, or backfilled as historical). The
// flusher reads it before reporting a new outcome, to decide
// first_for_install. The flusher is serialized, so nothing races it.
func (s *Store) WorkerPRUsageHistory(ctx context.Context) (associated, merged bool, err error) {
	err = s.readDB.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM usage_pr_measurement WHERE historical = 1 OR associated_reported = 1),
		EXISTS (SELECT 1 FROM usage_pr_measurement WHERE historical_merged = 1 OR merged_reported = 1)`).Scan(&associated, &merged)
	return associated, merged, err
}

// RecordWorkerPRMergedVia notes which client asked AO to merge a worker PR.
// Only an unreported merge is updated; a PR with no measurement row (an
// orchestrator's or a standalone session's) is left alone.
func (s *Store) RecordWorkerPRMergedVia(ctx context.Context, prURL, mergedVia string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `UPDATE usage_pr_measurement SET merged_via = ?
		WHERE merged_reported = 0 AND pr_key = (
			SELECT CASE WHEN provider_id <> '' AND provider <> ''
				THEN provider || ':' || host || ':' || provider_id ELSE url END
			FROM pr WHERE url = ?)`, mergedVia, prURL)
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
	if _, err := tx.ExecContext(ctx, `UPDATE usage_pr_measurement SET closed_reported = 1
		WHERE closed_reported = 0 AND pr_key IN (
			SELECT CASE WHEN provider_id <> '' AND provider <> ''
				THEN provider || ':' || host || ':' || provider_id ELSE url END
			FROM pr WHERE pr_state = 'closed'
		)`); err != nil {
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
