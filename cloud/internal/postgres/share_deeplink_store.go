package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/jackc/pgx/v5"
)

// Deep-link session shares (ao-app://share/<org>/<link>#<secret>) are a
// narrower sibling of project share links: always one named recipient,
// single-use, and short-lived. The owner picks the access level when minting:
// "view" (viewer, capped read-only) or "interact" (editor, the session's own
// mode — the recipient can type, message the agent, and edit files, but still
// cannot delete, restore, or re-share the session). Every rejection surfaces to
// the caller as a bare ErrForbidden — the specific reason is only written to
// the audit log, so a probing client cannot tell "wrong secret" from "expired"
// from "not addressed to you".

// SessionShareAccess is the access level a deep-link share grants.
type SessionShareAccess string

const (
	SessionShareView     SessionShareAccess = "view"
	SessionShareInteract SessionShareAccess = "interact"
)

// grantPolicy maps an access level onto the existing share-grant policy
// columns: role, interaction, and mode cap ("" = no cap beyond the session's
// own mode).
func (access SessionShareAccess) grantPolicy() (role, interaction, modeCap string) {
	if access == SessionShareInteract {
		return "editor", "interact", ""
	}
	return "viewer", "view", "read-only"
}

// CreateSessionShareDeepLink mints a single-use invitation to one session for
// one recipient email. Only an org member who created the session (or any
// member, for legacy sessions with no recorded creator) may share it.
func (s *Store) CreateSessionShareDeepLink(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID, recipientEmail string,
	access SessionShareAccess,
	ttl time.Duration,
) (domain.ShareLink, string, error) {
	role, interaction, modeCap := access.grantPolicy()
	recipientEmail = strings.ToLower(strings.TrimSpace(recipientEmail))
	if recipientEmail == strings.ToLower(strings.TrimSpace(principal.Email)) {
		return domain.ShareLink{}, "", ErrInvalid
	}
	token, tokenHash, err := newShareToken()
	if err != nil {
		return domain.ShareLink{}, "", err
	}
	recipients, err := json.Marshal([]string{recipientEmail})
	if err != nil {
		return domain.ShareLink{}, "", err
	}
	var link domain.ShareLink
	err = s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		var projectID string
		var createdBy *string
		err := tx.QueryRow(ctx,
			`SELECT project_id, created_by_user_id::text FROM ao_sessions WHERE org_id = $1 AND id = $2`,
			orgID, sessionID,
		).Scan(&projectID, &createdBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if createdBy != nil && *createdBy != principal.UserID {
			return ErrForbidden
		}
		if err := scanShareLink(tx.QueryRow(ctx,
			`INSERT INTO ao_project_share_links (
				org_id, project_id, session_id, created_by_user_id, token_hash,
				role, access_scope, recipients, interaction, mode_cap, single_use, expires_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, 'restricted', $7, $8, NULLIF($9, ''), true,
				now() + $10::interval
			)
			RETURNING `+shareLinkColumns,
			orgID, projectID, sessionID, principal.UserID, tokenHash, role, recipients, interaction, modeCap,
			intervalString(ttl),
		), &link); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO ao_audit_events (org_id, actor_user_id, action, resource_type, resource_id, metadata)
			VALUES ($1, $2, 'share.created', 'share_link', $3, $4)`,
			orgID, principal.UserID, link.ID,
			auditMetadata(map[string]any{"sessionId": sessionID, "recipient": recipientEmail, "role": role}),
		)
		return err
	})
	if err != nil {
		return domain.ShareLink{}, "", fmt.Errorf("create session share deep link: %w", err)
	}
	return link, token, nil
}

// PreviewSessionShareDeepLink validates an invitation for the calling user
// without consuming it, so the desktop app can render its consent dialog.
func (s *Store) PreviewSessionShareDeepLink(
	ctx context.Context,
	principal domain.Principal,
	orgID, linkID, token string,
) (domain.SessionShareInvite, error) {
	var invite domain.SessionShareInvite
	reason, err := s.withShareDeepLink(ctx, principal, orgID, linkID, token, false,
		func(tx pgx.Tx, link domain.ShareLink) error {
			invite = domain.SessionShareInvite{
				LinkID: link.ID, OrgID: link.OrgID, ProjectID: link.ProjectID, SessionID: link.SessionID,
				Role: link.Role,
			}
			if link.ExpiresAt != nil {
				invite.ExpiresAt = *link.ExpiresAt
			}
			return tx.QueryRow(ctx,
				`SELECT project.display_name, COALESCE(session_row.display_name, ''),
					inviter.email, inviter.display_name
				FROM ao_projects project
				JOIN ao_sessions session_row
					ON session_row.org_id = project.org_id AND session_row.id = $3
				JOIN ao_users inviter ON inviter.id = $4
				WHERE project.org_id = $1 AND project.id = $2`,
				link.OrgID, link.ProjectID, link.SessionID, link.CreatedByUserID,
			).Scan(&invite.ProjectName, &invite.SessionName, &invite.InviterEmail, &invite.InviterName)
		})
	if err != nil {
		s.recordShareDenied(ctx, principal, orgID, linkID, "preview", reason, err)
		return domain.SessionShareInvite{}, err
	}
	return invite, nil
}

// RedeemSessionShareDeepLink atomically consumes an invitation (active ->
// redeemed) and grants the calling user the link's access to its session.
func (s *Store) RedeemSessionShareDeepLink(
	ctx context.Context,
	principal domain.Principal,
	orgID, linkID, token string,
) (domain.SharedProject, error) {
	var shared domain.SharedProject
	reason, err := s.withShareDeepLink(ctx, principal, orgID, linkID, token, true,
		func(tx pgx.Tx, link domain.ShareLink) error {
			tag, err := tx.Exec(ctx,
				`UPDATE ao_project_share_links
				SET status = 'redeemed', redeemed_at = now(), redeemed_by_user_id = $2, updated_at = now()
				WHERE id = $1 AND status = 'active' AND single_use`,
				link.ID, principal.UserID,
			)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrForbidden
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ao_project_share_grants (
					share_link_id, org_id, project_id, session_id, user_id, shared_by_user_id, role,
					mode_cap, denied_commands
				) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)
				ON CONFLICT (user_id, org_id, project_id, COALESCE(session_id, '00000000-0000-0000-0000-000000000000'::uuid))
					WHERE status = 'active'
				DO UPDATE SET
					share_link_id = EXCLUDED.share_link_id,
					role = EXCLUDED.role,
					shared_by_user_id = EXCLUDED.shared_by_user_id,
					mode_cap = EXCLUDED.mode_cap,
					denied_commands = EXCLUDED.denied_commands,
					redeemed_at = now(),
					updated_at = now()`,
				link.ID, link.OrgID, link.ProjectID, link.SessionID, principal.UserID, link.CreatedByUserID,
				link.Role, link.ModeCap, link.DeniedCommands,
			); err != nil {
				return fmt.Errorf("create session share grant: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ao_audit_events (org_id, actor_user_id, action, resource_type, resource_id, metadata)
				VALUES ($1, $2, 'share.redeemed', 'share_link', $3, $4)`,
				link.OrgID, principal.UserID, link.ID,
				auditMetadata(map[string]any{"sessionId": link.SessionID, "role": link.Role}),
			); err != nil {
				return err
			}
			shared, err = scanSharedProject(tx.QueryRow(ctx,
				`SELECT `+sharedProjectColumns+sharedProjectFrom+`
				WHERE grant_row.org_id = $1 AND grant_row.project_id = $2 AND grant_row.user_id = $3
				  AND grant_row.status = 'active' AND grant_row.session_id = $4`,
				link.OrgID, link.ProjectID, principal.UserID, link.SessionID,
			))
			if errors.Is(err, pgx.ErrNoRows) {
				// The project was archived after the link was minted.
				return ErrForbidden
			}
			return err
		})
	if err != nil {
		s.recordShareDenied(ctx, principal, orgID, linkID, "redeem", reason, err)
		return domain.SharedProject{}, err
	}
	return shared, nil
}

// withShareDeepLink runs every invariant from the share design against the
// link and, only if all pass, calls fn inside the same transaction. It returns
// the internal denial reason (for the audit log) alongside ErrForbidden.
func (s *Store) withShareDeepLink(
	ctx context.Context,
	principal domain.Principal,
	orgID, linkID, token string,
	lock bool,
	fn func(tx pgx.Tx, link domain.ShareLink) error,
) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`SELECT set_config('ao.user_id', $1, true), set_config('ao.org_id', $2, true)`,
		principal.UserID, orgID,
	); err != nil {
		return "", err
	}
	query := `SELECT ` + shareLinkColumns + `, token_hash, single_use,
			(expires_at IS NOT NULL AND expires_at > now())
		FROM ao_project_share_links
		WHERE id = $1 AND org_id = $2`
	if lock {
		// Serializes concurrent redeems of one link; the loser then sees
		// status = 'redeemed' and is refused.
		query += ` FOR UPDATE`
	}
	var link domain.ShareLink
	var storedHash []byte
	var singleUse, unexpired bool
	var recipients []byte
	err = tx.QueryRow(ctx, query, linkID, orgID).Scan(
		&link.ID, &link.OrgID, &link.ProjectID, &link.SessionID, &link.CreatedByUserID,
		&link.Role, &link.Status, &link.AccessScope, &recipients, &link.Interaction, &link.ModeCap,
		&link.DeniedCommands, &link.ExpiresAt, &link.CreatedAt, &link.UpdatedAt,
		&storedHash, &singleUse, &unexpired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "unknown_link", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if len(recipients) > 0 {
		if err := json.Unmarshal(recipients, &link.Recipients); err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256([]byte(token))
	switch {
	case subtle.ConstantTimeCompare(sum[:], storedHash) != 1:
		return "secret_mismatch", ErrForbidden
	case !singleUse || link.SessionID == "":
		return "not_a_session_deeplink", ErrForbidden
	case link.Status != "active":
		return "status_" + link.Status, ErrForbidden
	case !unexpired:
		return "expired", ErrForbidden
	case link.CreatedByUserID == principal.UserID:
		return "self_redeem", ErrForbidden
	case !recipientsAllow(link.Recipients, principal.Email):
		return "email_mismatch", ErrForbidden
	}
	if err := fn(tx, link); err != nil {
		if errors.Is(err, ErrForbidden) {
			return "consumed_concurrently", err
		}
		return "", err
	}
	return "", tx.Commit(ctx)
}

// recordShareDenied appends a share.denied audit event for a refused preview
// or redeem. Best-effort: auditing must never change the response, and an
// orgId that does not exist simply records nothing.
func (s *Store) recordShareDenied(
	ctx context.Context,
	principal domain.Principal,
	orgID, linkID, stage, reason string,
	cause error,
) {
	if !errors.Is(cause, ErrForbidden) || reason == "" {
		return
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_audit_events (org_id, actor_user_id, action, resource_type, resource_id, metadata)
		SELECT $1::uuid, $2::uuid, 'share.denied', 'share_link', $3, $4
		WHERE EXISTS (SELECT 1 FROM ao_organizations WHERE id = $1::uuid)`,
		orgID, principal.UserID, linkID,
		auditMetadata(map[string]any{"stage": stage, "reason": reason}),
	); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

func auditMetadata(values map[string]any) []byte {
	encoded, err := json.Marshal(values)
	if err != nil {
		return []byte("{}")
	}
	return encoded
}

// LeaveSharedSession lets a recipient remove a session someone shared with
// them: it revokes the caller's own grant (never anyone else's) and leaves the
// owner's session untouched. Unknown ids and other users' grants are both
// ErrNotFound, so the call reveals nothing about grants the caller does not own.
func (s *Store) LeaveSharedSession(
	ctx context.Context,
	principal domain.Principal,
	grantID string,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.user_id', $1, true)`, principal.UserID); err != nil {
		return err
	}
	// The grants policy exposes a row to its recipient in any org, but only
	// accepts writes scoped to the grant's own org, so resolve that first.
	var orgID, sessionID string
	err = tx.QueryRow(ctx,
		`SELECT org_id, COALESCE(session_id::text, '') FROM ao_project_share_grants
		WHERE id = $1 AND user_id = $2 AND status = 'active'`,
		grantID, principal.UserID,
	).Scan(&orgID, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE ao_project_share_grants
		SET status = 'revoked', updated_at = now()
		WHERE id = $1 AND user_id = $2 AND status = 'active'`,
		grantID, principal.UserID,
	)
	if err != nil {
		return fmt.Errorf("leave shared session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_audit_events (org_id, actor_user_id, action, resource_type, resource_id, metadata)
		VALUES ($1, $2, 'share.left', 'share_grant', $3, $4)`,
		orgID, principal.UserID, grantID, auditMetadata(map[string]any{"sessionId": sessionID}),
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
