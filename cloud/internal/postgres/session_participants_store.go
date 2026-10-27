package postgres

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// MaxParticipantSessions caps how many sessions one participants request may
// name, so a board read stays bounded.
const MaxParticipantSessions = 100

// SessionParticipants returns, for each of sessionIDs principal may open and
// that someone has shared, the people in it: the creator first, then everyone
// a share grant lets in, in the order they joined, then any other member of
// the organization who has it open. A session principal may not open, or that
// nobody has shared, is left out.
func (s *Store) SessionParticipants(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	sessionIDs []string,
) (map[string][]domain.SessionParticipant, error) {
	result := map[string][]domain.SessionParticipant{}
	for _, sessionID := range sessionIDs {
		var participants []domain.SessionParticipant
		err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, _ sessionAccess) error {
			var err error
			participants, err = sessionParticipantsTx(ctx, tx, orgID, sessionID)
			return err
		})
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(participants) > 0 {
			result[sessionID] = participants
		}
	}
	return result, nil
}

func sessionParticipantsTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) ([]domain.SessionParticipant, error) {
	var owner domain.SessionParticipant
	var kind, requestedBy string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(session_row.created_by_user_id::text, ''), COALESCE(person.display_name, ''), COALESCE(person.avatar_url, ''),
			session_row.kind, COALESCE(session_row.requested_by_user_id::text, '')
		FROM ao_sessions session_row
		LEFT JOIN ao_users person ON person.id = session_row.created_by_user_id
		WHERE session_row.org_id = $1 AND session_row.id = $2`,
		orgID, sessionID,
	).Scan(&owner.UserID, &owner.Name, &owner.AvatarURL, &kind, &requestedBy); err != nil {
		return nil, err
	}
	owner.Role = "owner"

	// A grant for this session wins over one for its whole project.
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT ON (grant_row.user_id)
			grant_row.user_id::text, COALESCE(person.display_name, ''), COALESCE(person.avatar_url, ''), grant_row.role, grant_row.redeemed_at,
			grant_row.session_id IS NULL
		FROM ao_project_share_grants grant_row
		JOIN ao_sessions session_row
		  ON session_row.org_id = grant_row.org_id AND session_row.project_id = grant_row.project_id
		LEFT JOIN ao_users person ON person.id = grant_row.user_id
		WHERE grant_row.org_id = $1 AND session_row.id = $2 AND grant_row.status = 'active'
		  AND (grant_row.session_id = $2 OR grant_row.session_id IS NULL)
		ORDER BY grant_row.user_id, (grant_row.session_id IS NULL)`,
		orgID, sessionID,
	)
	if err != nil {
		return nil, err
	}
	type guest struct {
		participant domain.SessionParticipant
		joinedAt    time.Time
	}
	var guests []guest
	for rows.Next() {
		var g guest
		var wholeProject bool
		if err := rows.Scan(&g.participant.UserID, &g.participant.Name, &g.participant.AvatarURL, &g.participant.Role, &g.joinedAt, &wholeProject); err != nil {
			rows.Close()
			return nil, err
		}
		g.participant.Role = projectGrantRole(g.participant.Role, wholeProject, kind, requestedBy, g.participant.UserID)
		guests = append(guests, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(guests) == 0 {
		return nil, nil
	}
	sort.SliceStable(guests, func(i, j int) bool { return guests[i].joinedAt.Before(guests[j].joinedAt) })

	participants := []domain.SessionParticipant{}
	index := map[string]int{}
	add := func(participant domain.SessionParticipant) {
		if participant.UserID == "" {
			return
		}
		if _, seen := index[participant.UserID]; seen {
			return
		}
		participant.State = "idle"
		index[participant.UserID] = len(participants)
		participants = append(participants, participant)
	}
	add(owner)
	for _, g := range guests {
		add(g.participant)
	}

	viewers, err := listSessionViewersTx(ctx, tx, orgID, sessionID)
	if err != nil {
		return nil, err
	}
	for _, viewer := range viewers {
		if _, seen := index[viewer.UserID]; !seen {
			add(domain.SessionParticipant{UserID: viewer.UserID, Name: viewer.Name, AvatarURL: viewer.AvatarURL, Role: viewer.Role})
		}
		participants[index[viewer.UserID]].Present = true
	}

	composer, err := composerStateTx(ctx, tx, orgID, sessionID)
	if err != nil {
		return nil, err
	}
	for _, waiter := range composer.Waitlist {
		if at, ok := index[waiter.UserID]; ok {
			participants[at].State = "waiting"
		}
	}
	if composer.Holder != nil {
		if at, ok := index[composer.Holder.UserID]; ok {
			participants[at].State = "working"
		}
	}
	return participants, nil
}
