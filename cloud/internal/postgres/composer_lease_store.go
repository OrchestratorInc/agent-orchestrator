package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const (
	// ComposerLeaseTTL is how long a grant lasts without a renewal. A
	// holder's client renews every few seconds while its draft is in use.
	ComposerLeaseTTL = 15 * time.Second
	// ComposerHandoffWindow is how long the next person in line has to start
	// typing once the lease passes to them.
	ComposerHandoffWindow = 10 * time.Second
	// ComposerIdleRelease frees the lease from a holder who has not typed for
	// this long, so a forgotten draft cannot block everyone else.
	ComposerIdleRelease = 45 * time.Second
	// ComposerSweepInterval is how often expired leases are handed on.
	ComposerSweepInterval = 2 * time.Second
)

var (
	// ErrComposerLocked refuses a send or steer while someone else holds the
	// composer lease, or is waiting for it.
	ErrComposerLocked = errors.New("another participant holds the composer")
	// ErrComposerLeaseLost refuses a renewal from someone who no longer holds
	// the grant they present.
	ErrComposerLeaseLost = errors.New("composer lease lost")
)

// MaxComposerDraftBytes bounds a shared draft, as a message is bounded.
const MaxComposerDraftBytes = 65536

// composerLease is one session's lease row, locked for the transaction.
type composerLease struct {
	orgID, sessionID string
	holder           string
	epoch            int64
	expiresAt        *time.Time
	lastInputAt      *time.Time
	now              time.Time
	// The holder's draft as everyone else sees it. A change moves the
	// revision, which wakes the session's live streams.
	draftText     string
	draftCursor   int
	draftRevision int64
}

func (l *composerLease) expired() bool {
	return l.holder != "" && (l.expiresAt == nil || !l.now.Before(*l.expiresAt))
}

func (l *composerLease) free() {
	l.holder, l.expiresAt, l.lastInputAt = "", nil, nil
	l.clearDraft()
}

// clearDraft drops the shared draft: it belongs to one holder's turn.
func (l *composerLease) clearDraft() {
	if l.draftText != "" || l.draftCursor != 0 {
		l.draftText, l.draftCursor = "", 0
		l.draftRevision++
	}
}

// grant gives the lease to userID for ttl. Every grant is a new epoch, which
// fences off whoever held an earlier one.
func (l *composerLease) grant(userID string, ttl time.Duration) {
	expires := l.now.Add(ttl)
	now := l.now
	l.holder, l.epoch, l.expiresAt, l.lastInputAt = userID, l.epoch+1, &expires, &now
	l.clearDraft()
}

func (l *composerLease) heldBy(userID string, epoch int64) bool {
	return l.holder != "" && l.holder == userID && l.epoch == epoch
}

// lockComposerLeaseTx creates the session's lease row on first use and locks
// it. Every lease operation serializes on this row lock.
func lockComposerLeaseTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (*composerLease, error) {
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_composer_leases (session_id, org_id) VALUES ($2, $1)
		ON CONFLICT (session_id) DO NOTHING`,
		orgID, sessionID,
	); err != nil {
		return nil, normalizeConstraintError(err)
	}
	lease := &composerLease{orgID: orgID, sessionID: sessionID}
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(holder_user_id::text, ''), epoch, expires_at, last_input_at, now(),
			draft_text, draft_cursor, draft_revision
		FROM ao_composer_leases
		WHERE org_id = $1 AND session_id = $2
		FOR UPDATE`,
		orgID, sessionID,
	).Scan(&lease.holder, &lease.epoch, &lease.expiresAt, &lease.lastInputAt, &lease.now,
		&lease.draftText, &lease.draftCursor, &lease.draftRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return lease, err
}

func (l *composerLease) save(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx,
		`UPDATE ao_composer_leases
		SET holder_user_id = NULLIF($3, '')::uuid, epoch = $4, expires_at = $5,
			last_input_at = $6, draft_text = $7, draft_cursor = $8, draft_revision = $9,
			updated_at = now()
		WHERE org_id = $1 AND session_id = $2`,
		l.orgID, l.sessionID, l.holder, l.epoch, l.expiresAt, l.lastInputAt,
		l.draftText, l.draftCursor, l.draftRevision,
	)
	return err
}

// presentWaiterTx returns the first person in line who still has the session
// open, dropping anyone ahead of them who left. With pop, that person also
// leaves the line.
func presentWaiterTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, pop bool) (string, error) {
	if _, err := tx.Exec(ctx,
		`DELETE FROM ao_composer_waitlist waiter
		WHERE waiter.org_id = $1 AND waiter.session_id = $2
		  AND NOT EXISTS (
			SELECT 1 FROM ao_session_viewers viewer
			WHERE viewer.org_id = waiter.org_id AND viewer.session_id = waiter.session_id
			  AND viewer.user_id = waiter.user_id
			  AND viewer.seen_at > now() - $3::interval
		  )`,
		orgID, sessionID, intervalString(SessionViewerTTL),
	); err != nil {
		return "", err
	}
	var userID string
	err := tx.QueryRow(ctx,
		`SELECT user_id::text FROM ao_composer_waitlist
		WHERE org_id = $1 AND session_id = $2
		ORDER BY requested_at, user_id
		LIMIT 1`,
		orgID, sessionID,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil || !pop {
		return userID, err
	}
	_, err = tx.Exec(ctx,
		`DELETE FROM ao_composer_waitlist WHERE org_id = $1 AND session_id = $2 AND user_id = $3`,
		orgID, sessionID, userID,
	)
	return userID, err
}

// settle applies expiry, then hands a free lease to the first person in line
// who is still present.
func (l *composerLease) settle(ctx context.Context, tx pgx.Tx) error {
	if l.expired() {
		l.free()
	}
	if l.holder != "" {
		return nil
	}
	next, err := presentWaiterTx(ctx, tx, l.orgID, l.sessionID, true)
	if err != nil || next == "" {
		return err
	}
	l.grant(next, ComposerHandoffWindow)
	return nil
}

// stillHolds reports whether userID's grant at epoch stands. An expired grant
// stands until someone present is waiting: nobody else has had the lease, so
// its epoch still fences correctly.
func (l *composerLease) stillHolds(ctx context.Context, tx pgx.Tx, userID string, epoch int64) (bool, error) {
	if !l.heldBy(userID, epoch) {
		return false, nil
	}
	if !l.expired() {
		return true, nil
	}
	waiter, err := presentWaiterTx(ctx, tx, l.orgID, l.sessionID, false)
	return waiter == "", err
}

// claimComposerForSendTx admits a send or steer from userID, presenting the
// epoch it was granted (0 for none), and releases the lease to the next
// person in line. It admits the holder of that epoch, or anyone while the
// lease is free and nobody is waiting; everyone else gets ErrComposerLocked.
func claimComposerForSendTx(ctx context.Context, tx pgx.Tx, orgID, sessionID, userID string, epoch int64) error {
	lease, err := lockComposerLeaseTx(ctx, tx, orgID, sessionID)
	if err != nil {
		return err
	}
	holds, err := lease.stillHolds(ctx, tx, userID, epoch)
	if err != nil {
		return err
	}
	if !holds {
		if err := lease.settle(ctx, tx); err != nil {
			return err
		}
		if lease.holder != "" {
			return ErrComposerLocked
		}
	}
	// Sending ends the sender's turn; the next person in line gets it.
	lease.free()
	if err := lease.settle(ctx, tx); err != nil {
		return err
	}
	return lease.save(ctx, tx)
}

// composerStateTx reads the lease as everyone sees it.
func composerStateTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (domain.ComposerLease, error) {
	state := domain.ComposerLease{Waitlist: []domain.ComposerParticipant{}}
	var holderID, holderName, draftText string
	var draftCursor int
	var expiresIn *time.Duration
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(lease.holder_user_id::text, ''), COALESCE(person.display_name, ''), lease.epoch,
			CASE WHEN lease.holder_user_id IS NULL THEN NULL
				ELSE greatest(lease.expires_at - now(), interval '0') END,
			lease.draft_text, lease.draft_cursor
		FROM ao_composer_leases lease
		LEFT JOIN ao_users person ON person.id = lease.holder_user_id
		WHERE lease.org_id = $1 AND lease.session_id = $2`,
		orgID, sessionID,
	).Scan(&holderID, &holderName, &state.Epoch, &expiresIn, &draftText, &draftCursor)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return state, err
	}
	if holderID != "" {
		state.Holder = &domain.ComposerParticipant{UserID: holderID, Name: holderName}
		if expiresIn != nil {
			state.ExpiresIn = *expiresIn
		}
		if draftText != "" {
			state.Draft = &domain.ComposerDraft{Text: draftText, Cursor: draftCursor}
		}
	}
	rows, err := tx.Query(ctx,
		`SELECT waiter.user_id::text, COALESCE(person.display_name, '')
		FROM ao_composer_waitlist waiter
		LEFT JOIN ao_users person ON person.id = waiter.user_id
		WHERE waiter.org_id = $1 AND waiter.session_id = $2
		ORDER BY waiter.requested_at, waiter.user_id`,
		orgID, sessionID,
	)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var waiter domain.ComposerParticipant
		if err := rows.Scan(&waiter.UserID, &waiter.Name); err != nil {
			return state, err
		}
		state.Waitlist = append(state.Waitlist, waiter)
	}
	return state, rows.Err()
}

// composerOperation runs fn on the session's locked lease for someone who may
// edit it, saves the lease, and returns its state afterwards.
func (s *Store) composerOperation(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
	fn func(tx pgx.Tx, lease *composerLease) error,
) (domain.ComposerLease, error) {
	var state domain.ComposerLease
	var opErr error
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		if access.Role == "viewer" {
			return ErrForbidden
		}
		lease, err := lockComposerLeaseTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		// A refusal (opErr) still commits whatever settling it did, so a lapsed
		// lease is handed on even when the caller does not get it.
		opErr = fn(tx, lease)
		if opErr != nil && !errors.Is(opErr, ErrComposerLocked) && !errors.Is(opErr, ErrComposerLeaseLost) {
			return opErr
		}
		if err := lease.save(ctx, tx); err != nil {
			return err
		}
		state, err = composerStateTx(ctx, tx, orgID, sessionID)
		return err
	})
	if err != nil {
		return domain.ComposerLease{}, err
	}
	return state, opErr
}

// AcquireComposerLease grants principal the lease when it is free and nobody
// is ahead of them in line, or renews it when they already hold it. Otherwise
// it returns the current state with ErrComposerLocked.
func (s *Store) AcquireComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		if err := lease.settle(ctx, tx); err != nil {
			return err
		}
		switch lease.holder {
		case principal.UserID:
			expires, now := lease.now.Add(ComposerLeaseTTL), lease.now
			lease.expiresAt, lease.lastInputAt = &expires, &now
		case "":
			lease.grant(principal.UserID, ComposerLeaseTTL)
		default:
			return ErrComposerLocked
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM ao_composer_waitlist WHERE org_id = $1 AND session_id = $2 AND user_id = $3`,
			orgID, sessionID, principal.UserID,
		)
		return err
	})
}

// RenewComposerLease extends principal's grant at epoch. typing reports a
// keystroke since the last renewal; a holder who has not typed for
// ComposerIdleRelease loses the lease. A grant that was taken over, or lapsed
// while someone was waiting, returns ErrComposerLeaseLost.
func (s *Store) RenewComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string, epoch int64, typing bool) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		holds, err := lease.stillHolds(ctx, tx, principal.UserID, epoch)
		if err != nil {
			return err
		}
		if !holds {
			if err := lease.settle(ctx, tx); err != nil {
				return err
			}
			return ErrComposerLeaseLost
		}
		if typing {
			now := lease.now
			lease.lastInputAt = &now
		}
		if lease.lastInputAt == nil || lease.now.Sub(*lease.lastInputAt) >= ComposerIdleRelease {
			lease.free()
			if err := lease.settle(ctx, tx); err != nil {
				return err
			}
			return ErrComposerLeaseLost
		}
		expires := lease.now.Add(ComposerLeaseTTL)
		lease.expiresAt = &expires
		return nil
	})
}

// ReleaseComposerLease gives up principal's grant at epoch and hands the lease
// to the next person in line. Releasing a grant no longer held does nothing.
func (s *Store) ReleaseComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string, epoch int64) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		if lease.heldBy(principal.UserID, epoch) {
			lease.free()
		}
		return lease.settle(ctx, tx)
	})
}

// PublishComposerDraft shares what principal, holding the grant at epoch, is
// typing, so everyone else in the session sees it live. It counts as a
// keystroke: it renews the grant. Anyone who does not hold that grant gets
// ErrComposerLeaseLost, so only the writer's own text is ever shown.
func (s *Store) PublishComposerDraft(ctx context.Context, principal domain.Principal, orgID, sessionID string, epoch int64, text string, cursor int) (domain.ComposerLease, error) {
	if len(text) > MaxComposerDraftBytes || cursor < 0 {
		return domain.ComposerLease{}, ErrInvalid
	}
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		holds, err := lease.stillHolds(ctx, tx, principal.UserID, epoch)
		if err != nil {
			return err
		}
		if !holds {
			if err := lease.settle(ctx, tx); err != nil {
				return err
			}
			return ErrComposerLeaseLost
		}
		expires, now := lease.now.Add(ComposerLeaseTTL), lease.now
		lease.expiresAt, lease.lastInputAt = &expires, &now
		if text != lease.draftText || cursor != lease.draftCursor {
			lease.draftText, lease.draftCursor = text, cursor
			lease.draftRevision++
		}
		return nil
	})
}

// RequestComposerTurn puts principal in line for the lease. If the lease is
// free and they are first, it passes to them at once.
func (s *Store) RequestComposerTurn(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		if lease.holder == principal.UserID && !lease.expired() {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO ao_composer_waitlist (session_id, org_id, user_id) VALUES ($2, $1, $3)
			ON CONFLICT (session_id, user_id) DO NOTHING`,
			orgID, sessionID, principal.UserID,
		); err != nil {
			return normalizeConstraintError(err)
		}
		return lease.settle(ctx, tx)
	})
}

// LeaveComposerQueue takes principal out of line.
func (s *Store) LeaveComposerQueue(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM ao_composer_waitlist WHERE org_id = $1 AND session_id = $2 AND user_id = $3`,
			orgID, sessionID, principal.UserID,
		)
		return err
	})
}

// TakeOverComposerLease gives principal the lease from whoever holds it. Only
// the session's creator and the organization's owners and admins may.
func (s *Store) TakeOverComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error) {
	return s.composerOperation(ctx, principal, orgID, sessionID, func(tx pgx.Tx, lease *composerLease) error {
		allowed, err := mayManageSessionTx(ctx, tx, orgID, sessionID, principal.UserID)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbidden
		}
		lease.grant(principal.UserID, ComposerLeaseTTL)
		_, err = tx.Exec(ctx,
			`DELETE FROM ao_composer_waitlist WHERE org_id = $1 AND session_id = $2 AND user_id = $3`,
			orgID, sessionID, principal.UserID,
		)
		return err
	})
}

// mayManageSessionTx reports whether userID manages the session: its
// creator, or an owner or admin of its organization. Managers may take the
// composer lease from its holder and set the session's chat settings.
func mayManageSessionTx(ctx context.Context, tx pgx.Tx, orgID, sessionID, userID string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx,
		`SELECT session.created_by_user_id IS NOT DISTINCT FROM $3::uuid OR EXISTS (
			SELECT 1 FROM ao_org_memberships membership
			WHERE membership.org_id = $1 AND membership.user_id = $3::uuid
			  AND membership.status = 'active' AND membership.role IN ('owner', 'admin')
		)
		FROM ao_sessions session
		WHERE session.org_id = $1 AND session.id = $2`,
		orgID, sessionID, userID,
	).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return allowed, err
}

// SweepComposerLeases hands on leases whose grant expired, so everyone sees
// the lease free or passed to the next person without waiting for another
// request. It returns how many leases it settled.
func (s *Store) SweepComposerLeases(ctx context.Context) (int, error) {
	type expiredLease struct{ orgID, sessionID string }
	var expired []expiredLease
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT org_id::text, session_id::text FROM ao_composer_leases
			WHERE holder_user_id IS NOT NULL AND expires_at <= now()
			ORDER BY expires_at
			LIMIT 200`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var lease expiredLease
			if err := rows.Scan(&lease.orgID, &lease.sessionID); err != nil {
				return err
			}
			expired = append(expired, lease)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, item := range expired {
		err := s.withOrg(ctx, item.orgID, func(tx pgx.Tx) error {
			lease, err := lockComposerLeaseTx(ctx, tx, item.orgID, item.sessionID)
			if err != nil || !lease.expired() {
				return err
			}
			if err := lease.settle(ctx, tx); err != nil {
				return err
			}
			settled++
			return lease.save(ctx, tx)
		})
		if err != nil {
			return settled, err
		}
	}
	return settled, nil
}

// mayDirectTurnTx reports whether userID may stop a turn or answer its
// approval prompts: the person whose message started it, whoever holds the
// composer lease, the session's creator, and the organization's owners and
// admins. Anyone else in a shared session would be acting over the person the
// turn belongs to.
func mayDirectTurnTx(ctx context.Context, tx pgx.Tx, orgID, sessionID, turnID, userID string) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx,
		`SELECT session.created_by_user_id IS NOT DISTINCT FROM $4::uuid
			OR EXISTS (
				SELECT 1 FROM ao_org_memberships membership
				WHERE membership.org_id = $1 AND membership.user_id = $4::uuid
				  AND membership.status = 'active' AND membership.role IN ('owner', 'admin')
			)
			OR EXISTS (
				SELECT 1 FROM ao_composer_leases lease
				WHERE lease.org_id = $1 AND lease.session_id = $2
				  AND lease.holder_user_id = $4::uuid AND lease.expires_at > now()
			)
			OR EXISTS (
				SELECT 1 FROM ao_turns turn
				JOIN ao_events message
					ON message.org_id = turn.org_id AND message.session_id = turn.session_id
					AND message.sequence = turn.user_message_sequence
				WHERE turn.org_id = $1 AND turn.session_id = $2 AND turn.id = $3::uuid
				  AND message.payload->>'authorUserId' = $4::text
			)
		FROM ao_sessions session
		WHERE session.org_id = $1 AND session.id = $2`,
		orgID, sessionID, turnID, userID,
	).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return allowed, err
}

// ComposerTerminalInput admits a keystroke userID typed into the session's
// agent terminal, where typing is talking to the agent: it takes or renews
// the composer lease for them, or refuses while someone else holds it or is
// waiting for it. The caller has already authorized the terminal.
func (s *Store) ComposerTerminalInput(ctx context.Context, orgID, sessionID, userID string) (bool, error) {
	var allowed bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		lease, err := lockComposerLeaseTx(ctx, tx, orgID, sessionID)
		if err != nil {
			return err
		}
		holds, err := lease.stillHolds(ctx, tx, userID, lease.epoch)
		if err != nil {
			return err
		}
		if holds {
			expires, now := lease.now.Add(ComposerLeaseTTL), lease.now
			lease.expiresAt, lease.lastInputAt = &expires, &now
		} else {
			if err := lease.settle(ctx, tx); err != nil {
				return err
			}
			switch lease.holder {
			case "":
				lease.grant(userID, ComposerLeaseTTL)
				if _, err := tx.Exec(ctx,
					`DELETE FROM ao_composer_waitlist WHERE org_id = $1 AND session_id = $2 AND user_id = $3`,
					orgID, sessionID, userID,
				); err != nil {
					return err
				}
			case userID:
				// The lease was just handed to them from the line.
				now := lease.now
				lease.lastInputAt = &now
			default:
				return lease.save(ctx, tx)
			}
		}
		allowed = true
		return lease.save(ctx, tx)
	})
	return allowed, err
}

// sessionChatSettingsTx reads the chat settings the session's owner set.
func sessionChatSettingsTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string) (domain.ChatTurnSettings, error) {
	var raw []byte
	err := tx.QueryRow(ctx,
		`SELECT chat_settings FROM ao_sessions WHERE org_id = $1 AND id = $2`,
		orgID, sessionID,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChatTurnSettings{}, ErrNotFound
	}
	if err != nil {
		return domain.ChatTurnSettings{}, err
	}
	var settings domain.ChatTurnSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return domain.ChatTurnSettings{}, err
	}
	return settings, nil
}

// SetSessionChatSettings records the chat settings the session's turns run
// with. Only the session's owner may (see mayManageSessionTx); everyone else
// in a shared session sees them and their turns use them.
func (s *Store) SetSessionChatSettings(ctx context.Context, principal domain.Principal, orgID, sessionID string, settings domain.ChatTurnSettings) (domain.ChatTurnSettings, error) {
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		if access.Role == "viewer" {
			return ErrForbidden
		}
		manager, err := mayManageSessionTx(ctx, tx, orgID, sessionID, principal.UserID)
		if err != nil {
			return err
		}
		if !manager {
			return ErrForbidden
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE ao_sessions SET chat_settings = $3 WHERE org_id = $1 AND id = $2`,
			orgID, sessionID, raw,
		)
		return err
	})
	return settings, err
}

// turnSettingsForSenderTx decides the settings a person's message runs with.
// The session's owner sends with their own choice. Anyone else gets the
// settings the owner set, or, while the owner has set none, their own choice
// only if they belong to the organization: a share guest never picks.
func turnSettingsForSenderTx(ctx context.Context, tx pgx.Tx, orgID, sessionID, userID string, access sessionAccess, requested domain.ChatTurnSettings) (domain.ChatTurnSettings, error) {
	manager, err := mayManageSessionTx(ctx, tx, orgID, sessionID, userID)
	if err != nil || manager {
		return requested, err
	}
	stored, err := sessionChatSettingsTx(ctx, tx, orgID, sessionID)
	if err != nil {
		return domain.ChatTurnSettings{}, err
	}
	if stored != (domain.ChatTurnSettings{}) || access.Role != "member" {
		return stored, nil
	}
	return requested, nil
}
