package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// SessionViewerTTL is how long an open live stream counts as present without
// a refresh. Streams refresh well inside it (see SessionViewerRefresh), so
// only a stream whose replica died drops out this way.
const SessionViewerTTL = time.Minute

// SessionViewerRefresh is how often an open live stream refreshes its row.
const SessionViewerRefresh = 20 * time.Second

// JoinSessionViewers records that principal opened a live stream on the
// session, under connectionID. It also prunes rows a dead replica left.
func (s *Store) JoinSessionViewers(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID, connectionID string,
) error {
	return s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM ao_session_viewers
			WHERE org_id = $1 AND session_id = $2 AND seen_at < now() - $3::interval`,
			orgID, sessionID, intervalString(5*SessionViewerTTL),
		); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO ao_session_viewers (connection_id, org_id, session_id, user_id, role)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (connection_id) DO UPDATE SET seen_at = now()`,
			connectionID, orgID, sessionID, principal.UserID, access.Role,
		)
		return err
	})
}

// RefreshSessionViewer keeps an open live stream's row from expiring.
func (s *Store) RefreshSessionViewer(ctx context.Context, orgID, connectionID string) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_session_viewers SET seen_at = now()
			WHERE org_id = $1 AND connection_id = $2`,
			orgID, connectionID,
		)
		return err
	})
}

// LeaveSessionViewers removes a closed live stream's row.
func (s *Store) LeaveSessionViewers(ctx context.Context, orgID, connectionID string) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM ao_session_viewers WHERE org_id = $1 AND connection_id = $2`,
			orgID, connectionID,
		)
		return err
	})
}

// SessionLiveState returns the session's live state as principal may see it:
// one viewer per person with a fresh live stream, ordered by when they first
// joined, and the composer lease.
func (s *Store) SessionLiveState(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
) (domain.SessionLiveState, error) {
	state := domain.SessionLiveState{Viewers: []domain.SessionViewer{}}
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, _ sessionAccess) error {
		viewers, err := listSessionViewersTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		state.Viewers = viewers
		if state.Composer, err = composerStateTx(ctx, tx, orgID, sessionID); err != nil {
			return err
		}
		if state.CanTakeOver, err = mayManageSessionTx(ctx, tx, orgID, sessionID, principal.UserID); err != nil {
			return err
		}
		state.CanConfigure = state.CanTakeOver
		if state.ChatSettings, err = sessionChatSettingsTx(ctx, tx, orgID, sessionID); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT COALESCE((
				SELECT max(request.updated_at)::text || '/' || count(*)::text
				FROM ao_merge_requests request
				WHERE request.org_id = session_row.org_id AND request.project_id = session_row.project_id
				HAVING count(*) > 0
			), '')
			FROM ao_sessions session_row
			WHERE session_row.org_id = $1 AND session_row.id = $2`,
			orgID, sessionID,
		).Scan(&state.MergeRequestsVersion)
	})
	return state, err
}

func listSessionViewersTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) ([]domain.SessionViewer, error) {
	// A person with several streams appears once, with their strongest role.
	rows, err := tx.Query(ctx,
		`SELECT viewer.user_id::text, COALESCE(person.display_name, ''), COALESCE(person.avatar_url, ''),
			CASE
				WHEN bool_or(viewer.role = 'member') THEN 'member'
				WHEN bool_or(viewer.role = 'editor') THEN 'editor'
				ELSE 'viewer'
			END
		FROM ao_session_viewers viewer
		LEFT JOIN ao_users person ON person.id = viewer.user_id
		WHERE viewer.org_id = $1 AND viewer.session_id = $2
		  AND viewer.seen_at > now() - $3::interval
		GROUP BY viewer.user_id, person.display_name, person.avatar_url
		ORDER BY min(viewer.connected_at), viewer.user_id`,
		orgID, sessionID, intervalString(SessionViewerTTL),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	viewers := []domain.SessionViewer{}
	for rows.Next() {
		var viewer domain.SessionViewer
		if err := rows.Scan(&viewer.UserID, &viewer.Name, &viewer.AvatarURL, &viewer.Role); err != nil {
			return nil, err
		}
		viewers = append(viewers, viewer)
	}
	return viewers, rows.Err()
}
