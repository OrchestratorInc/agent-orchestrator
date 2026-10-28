package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const mergeRequestColumns = `
	request.id::text, request.org_id::text, request.project_id::text, request.session_id::text,
	COALESCE(session_row.display_name, ''), request.pull_request_id::text,
	pr.number, pr.url, pr.title, pr.repository,
	request.requested_by_user_id::text, COALESCE(requester.display_name, ''),
	request.status, COALESCE(request.decided_by_user_id::text, ''), COALESCE(decider.display_name, ''),
	request.decided_at, request.created_at`

const mergeRequestFrom = `
	FROM ao_merge_requests request
	JOIN ao_pull_requests pr ON pr.org_id = request.org_id AND pr.id = request.pull_request_id
	LEFT JOIN ao_sessions session_row ON session_row.org_id = request.org_id AND session_row.id = request.session_id
	LEFT JOIN ao_users requester ON requester.id = request.requested_by_user_id
	LEFT JOIN ao_users decider ON decider.id = request.decided_by_user_id`

func scanMergeRequest(row pgx.Row) (domain.MergeRequest, error) {
	var request domain.MergeRequest
	err := row.Scan(
		&request.ID, &request.OrgID, &request.ProjectID, &request.SessionID,
		&request.SessionName, &request.PullRequestID,
		&request.PRNumber, &request.PRURL, &request.PRTitle, &request.Repository,
		&request.RequestedByUserID, &request.RequestedByName,
		&request.Status, &request.DecidedByUserID, &request.DecidedByName,
		&request.DecidedAt, &request.CreatedAt,
	)
	return request, err
}

func getMergeRequestTx(ctx context.Context, tx pgx.Tx, orgID, requestID string) (domain.MergeRequest, error) {
	request, err := scanMergeRequest(tx.QueryRow(ctx,
		`SELECT `+mergeRequestColumns+mergeRequestFrom+` WHERE request.org_id = $1 AND request.id = $2`,
		orgID, requestID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MergeRequest{}, ErrNotFound
	}
	return request, err
}

// RequestPullRequestMerge asks the owner of a shared session to merge one of
// the pull requests it shows. Only someone the project or session was shared
// with, who may write in it, asks; a member of the organization merges
// directly. Asking again while a request is pending returns that request.
// The session's creator is notified.
func (s *Store) RequestPullRequestMerge(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
	number int,
	prURL string,
) (domain.MergeRequest, error) {
	var request domain.MergeRequest
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		switch access.Role {
		case "viewer":
			return ErrForbidden
		case "member":
			return fmt.Errorf("%w: members merge pull requests themselves", ErrConflict)
		}
		pr, err := pullRequestShownBySessionTx(ctx, tx, orgID, sessionID, number, prURL)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if pr.State != "open" {
			return fmt.Errorf("%w: the pull request is not open", ErrConflict)
		}
		var requestID string
		var created bool
		err = tx.QueryRow(ctx,
			`WITH inserted AS (
				INSERT INTO ao_merge_requests (org_id, project_id, session_id, pull_request_id, requested_by_user_id)
				SELECT session_row.org_id, session_row.project_id, session_row.id, $3, $4
				FROM ao_sessions session_row
				WHERE session_row.org_id = $1 AND session_row.id = $2
				ON CONFLICT (org_id, pull_request_id) WHERE status = 'pending' DO NOTHING
				RETURNING id
			)
			SELECT id::text, true FROM inserted
			UNION ALL
			SELECT id::text, false FROM ao_merge_requests
			WHERE org_id = $1 AND pull_request_id = $3 AND status = 'pending'
			  AND NOT EXISTS (SELECT 1 FROM inserted)
			LIMIT 1`,
			orgID, sessionID, pr.ID, principal.UserID,
		).Scan(&requestID, &created)
		if err != nil {
			return err
		}
		if request, err = getMergeRequestTx(ctx, tx, orgID, requestID); err != nil {
			return err
		}
		if !created {
			return nil
		}
		recipient, err := sessionCreatorTx(ctx, tx, orgID, sessionID)
		if err != nil || recipient == "" {
			return err
		}
		return notifyMergeRequestTx(ctx, tx, request, recipient, "merge_requested",
			fmt.Sprintf("%s wants to merge #%d", nameOr(request.RequestedByName, "Someone"), request.PRNumber),
			request.PRTitle)
	})
	return request, err
}

// ListProjectMergeRequests lists a project's merge requests, pending first,
// then newest first. A member of the organization sees them all; someone the
// project was shared with sees their own.
func (s *Store) ListProjectMergeRequests(ctx context.Context, principal domain.Principal, orgID, projectID string) ([]domain.MergeRequest, error) {
	requests := []domain.MergeRequest{}
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, principal.UserID); err != nil {
			return err
		}
		member, err := orgMemberTx(ctx, tx, principal, orgID)
		if err != nil {
			return err
		}
		onlyMine := ""
		if !member {
			var shared bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM ao_project_share_grants
				WHERE org_id = $1 AND project_id = $2 AND user_id = $3::uuid AND status = 'active')`,
				orgID, projectID, principal.UserID,
			).Scan(&shared); err != nil {
				return err
			}
			if !shared {
				return ErrForbidden
			}
			onlyMine = principal.UserID
		}
		rows, err := tx.Query(ctx,
			`SELECT `+mergeRequestColumns+mergeRequestFrom+`
			WHERE request.org_id = $1 AND request.project_id = $2
			  AND ($3 = '' OR request.requested_by_user_id = NULLIF($3, '')::uuid)
			ORDER BY (request.status = 'pending') DESC, request.created_at DESC
			LIMIT 100`,
			orgID, projectID, onlyMine,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			request, err := scanMergeRequest(rows)
			if err != nil {
				return err
			}
			requests = append(requests, request)
		}
		return rows.Err()
	})
	return requests, err
}

// PendingMergeRequestForDecision authorizes principal to approve or reject a
// pending merge request: the session's creator, or an owner or admin of its
// organization.
func (s *Store) PendingMergeRequestForDecision(ctx context.Context, principal domain.Principal, orgID, requestID string) (domain.MergeRequest, error) {
	var request domain.MergeRequest
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if request, err = getMergeRequestTx(ctx, tx, orgID, requestID); err != nil {
			return err
		}
		allowed, err := mayManageSessionTx(ctx, tx, orgID, request.SessionID, principal.UserID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		if request.Status != "pending" {
			return fmt.Errorf("%w: the request was already %s", ErrConflict, request.Status)
		}
		return nil
	})
	return request, err
}

// DecideMergeRequest records the owner's decision on a pending request,
// "merged" after the merge went through or "rejected", and tells the guest
// who asked. The owner's request notification is resolved.
func (s *Store) DecideMergeRequest(ctx context.Context, principal domain.Principal, orgID, requestID, status string) (domain.MergeRequest, error) {
	if status != "merged" && status != "rejected" {
		return domain.MergeRequest{}, ErrInvalid
	}
	var request domain.MergeRequest
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		pending, err := getMergeRequestTx(ctx, tx, orgID, requestID)
		if err != nil {
			return err
		}
		allowed, err := mayManageSessionTx(ctx, tx, orgID, pending.SessionID, principal.UserID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_merge_requests
			SET status = $3, decided_by_user_id = $4::uuid, decided_at = now(), updated_at = now()
			WHERE org_id = $1 AND id = $2 AND status = 'pending'`,
			orgID, requestID, status, principal.UserID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: the request was already decided", ErrConflict)
		}
		if request, err = getMergeRequestTx(ctx, tx, orgID, requestID); err != nil {
			return err
		}
		owner, err := sessionCreatorTx(ctx, tx, orgID, request.SessionID)
		if err != nil {
			return err
		}
		if owner != "" {
			if err := resolveMergeRequestNotificationTx(ctx, tx, request, owner); err != nil {
				return err
			}
		}
		kind, title := "merge_request_merged", fmt.Sprintf("#%d was merged", request.PRNumber)
		if status == "rejected" {
			kind, title = "merge_request_rejected", fmt.Sprintf("Your request to merge #%d was declined", request.PRNumber)
		}
		return notifyMergeRequestTx(ctx, tx, request, request.RequestedByUserID, kind, title, request.PRTitle)
	})
	return request, err
}

func sessionCreatorTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (string, error) {
	var creator string
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(created_by_user_id::text, '') FROM ao_sessions WHERE org_id = $1 AND id = $2`,
		orgID, sessionID,
	).Scan(&creator)
	return creator, err
}

func nameOr(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return name
}

// notifyMergeRequestTx tells recipient about a merge request, in the same
// notification feed as pull request updates. It scopes the transaction to the
// recipient, as ao_notifications' row security requires, so it runs last.
func notifyMergeRequestTx(ctx context.Context, tx pgx.Tx, request domain.MergeRequest, recipient, kind, title, body string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, recipient); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{
		"mergeRequestId": request.ID, "pullRequestId": request.PullRequestID, "pullRequestUrl": request.PRURL,
		"pullRequestNumber": request.PRNumber, "repository": request.Repository, "requestedBy": request.RequestedByName,
	})
	dedupe := kind + ":" + request.ID
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO ao_notifications(org_id, recipient_user_id, project_id, session_id, pull_request_id, source, type, title, body, metadata, dedupe_key, source_event_id, status)
		VALUES ($1, $2, $3, $4, $5, 'cloud', $6, $7, $8, $9, $10, $10, 'unread')
		ON CONFLICT (org_id, recipient_user_id, dedupe_key) WHERE resolved_at IS NULL
		DO UPDATE SET status = 'unread', updated_at = now()
		RETURNING id::text`,
		request.OrgID, recipient, request.ProjectID, request.SessionID, request.PullRequestID, kind, title, body, metadata, dedupe,
	).Scan(&id); err != nil {
		return err
	}
	return publishPullRequestNotificationEventTx(ctx, tx, request.OrgID, recipient, id, "notification_created", dedupe)
}

func resolveMergeRequestNotificationTx(ctx context.Context, tx pgx.Tx, request domain.MergeRequest, owner string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, owner); err != nil {
		return err
	}
	dedupe := "merge_requested:" + request.ID
	var id string
	err := tx.QueryRow(ctx,
		`UPDATE ao_notifications SET resolved_at = now(), updated_at = now()
		WHERE org_id = $1 AND recipient_user_id = $2 AND dedupe_key = $3 AND resolved_at IS NULL
		RETURNING id::text`,
		request.OrgID, owner, dedupe,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return publishPullRequestNotificationEventTx(ctx, tx, request.OrgID, owner, id, "notification_resolved", "resolved:"+dedupe)
}
