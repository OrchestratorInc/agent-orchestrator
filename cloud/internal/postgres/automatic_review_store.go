package postgres

import (
	"context"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// AutomaticReviewCandidates reads webhook-maintained facts, not GitHub. Each
// tenant's review history is read under its own RLS context.
func (s *Store) AutomaticReviewCandidates(ctx context.Context) ([]domain.PullRequest, error) {
	var sessions [][2]string
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT session.org_id::text, session.id::text
			FROM ao_sessions session JOIN ao_sandboxes sandbox
			ON sandbox.org_id=session.org_id AND sandbox.session_id=session.id
			WHERE session.kind='worker' AND session.auto_review_enabled
			AND NOT session.is_terminated AND session.activity_state='idle'
			AND sandbox.desired_state IN ('running','paused')`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var session [2]string
			if err := rows.Scan(&session[0], &session[1]); err != nil {
				return err
			}
			sessions = append(sessions, session)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var candidates []domain.PullRequest
	for _, session := range sessions {
		err = s.withOrg(ctx, session[0], func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT `+pullRequestColumns+` FROM ao_pull_requests pr
				WHERE org_id=$1 AND session_id=$2 AND state='open' AND NOT draft AND head_sha<>''
				AND NOT EXISTS (SELECT 1 FROM ao_review_runs run
					WHERE run.org_id=pr.org_id AND run.pull_request_id=pr.id AND run.target_sha=pr.head_sha)
				AND EXISTS (SELECT 1 FROM ao_projects project WHERE project.org_id=pr.org_id
					AND project.archived_at IS NULL AND project.id=(SELECT project_id FROM ao_sessions WHERE org_id=$1 AND id=$2))`, session[0], session[1])
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				pr, err := scanPullRequest(rows)
				if err != nil {
					return err
				}
				candidates = append(candidates, pr)
			}
			return rows.Err()
		})
		if err != nil {
			return nil, err
		}
	}
	return candidates, nil
}
