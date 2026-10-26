package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/interfacehandoff"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrTransitionInProgress reports a session that already owns an active
	// interface transition. Only one controller handoff may run at a time.
	ErrTransitionInProgress = errors.New("interface transition already in progress")
	// ErrTransitionNotFound reports a transition id or notice that does not
	// exist for the requested session.
	ErrTransitionNotFound = errors.New("interface transition not found")
	// ErrTransitionNotCancellable reports a transition the coordinator has
	// advanced into a phase where cancelling would leave controller ownership
	// ambiguous.
	ErrTransitionNotCancellable = errors.New("interface transition is not cancellable")
	// ErrTransitionNoticeNotAcknowledgeable reports an acknowledgement attempt
	// against a transition that is not in a failed or recovered state.
	ErrTransitionNoticeNotAcknowledgeable = errors.New("interface transition notice is not acknowledgeable")
	// ErrTransitionStale fences a phase advance from a coordinator that lost
	// ownership to a newer replica's single-owner claim.
	ErrTransitionStale = errors.New("interface transition was claimed by another coordinator")
	// ErrInvalidTransition reports invalid input at the storage boundary.
	ErrInvalidTransition = errors.New("invalid interface transition")
)

const renewCoordinatedInterfaceClaimSQL = `UPDATE ao_interface_transitions
			SET claimed_by = $1, claimed_at = now(), updated_at = now()
			WHERE id = $2 AND claimed_by = $1`

const commitCoordinatedSessionInterfaceSQL = `UPDATE ao_sessions AS session
			SET interface = $1,
				activity_state = 'idle',
				activity_source_request_id = NULL,
				activity_blocked_tool_name = '',
				activity_blocked_tool_use_id = '',
				updated_at = now()
			FROM ao_interface_transitions AS transition
			WHERE transition.id = $2
			  AND transition.org_id = $3
			  AND transition.claimed_by = $4
			  AND transition.phase = 'source_stopped'
			  AND transition.org_id = session.org_id
			  AND transition.session_id = session.id
			RETURNING session.id`

// StartSessionInterfaceTransition durably claims a session for an interface
// handoff. It returns the committed transition and whether a new row was
// created. When a session already owns an active transition, no work is done
// and ErrTransitionInProgress is returned.
func (s *Store) StartSessionInterfaceTransition(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
	source, target domain.SessionInterface,
	policy domain.SessionInterfaceTransitionPolicy,
	nativeConversationID string,
	settings ...domain.ChatTurnSettings,
) (domain.SessionInterfaceTransition, error) {
	if !source.Valid() || !target.Valid() || source == target {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: source %q, target %q", ErrInvalidTransition, source, target)
	}
	if !policy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: policy %q", ErrInvalidTransition, policy)
	}
	var transition domain.SessionInterfaceTransition
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		// Message admission updates this session row before choosing its route.
		// Hold the same lock before checking for a transition so a concurrent
		// message is either held by this handoff or settled before it starts.
		var lockedSession int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM ao_sessions
			WHERE org_id = $1 AND id = $2 AND is_terminated = false
			FOR UPDATE`, orgID, sessionID).Scan(&lockedSession); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var active bool
		if err := tx.QueryRow(
			ctx,
			`SELECT EXISTS (
				SELECT 1 FROM ao_interface_transitions t
				WHERE t.session_id = $1 AND t.org_id = $2
					AND t.phase NOT IN (
						'completed', 'failed', 'cancelled'
					)
			)`,
			sessionID,
			orgID,
		).Scan(&active); err != nil {
			return err
		}
		if active {
			return ErrTransitionInProgress
		}
		var err error
		var selected domain.ChatTurnSettings
		if len(settings) > 0 {
			selected = settings[0]
		}
		transition, err = insertInterfaceTransition(
			ctx, tx, orgID, sessionID, source, target, policy, nativeConversationID, selected.Model, selected.ReasoningEffort,
		)
		if err != nil {
			return err
		}
		if source == domain.SessionInterfaceChat && policy == domain.SessionInterfaceTransitionInterrupt {
			return interruptQueuedChatTurns(ctx, tx, orgID, sessionID)
		}
		return nil
	})
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	return transition, nil
}

// Stop-now cancels turns that have not reached the Chat controller. The active
// turn is interrupted by the worker; queued turns must not be claimed by the
// replacement TUI after the handoff commits.
//
// The session's task is the exception while it never ran: a session created
// in Chat queues its create prompt as its first turn (marked sessionTask, see
// queueInitialChatTurnTx) and launches its terminal without it, so that turn
// stays queued for the terminal's turn forwarder, which delivers it once the
// agent starts. A session whose terminal launch still carries the prompt
// (created before Chat sessions launched without it) gets it on the command
// line, so its turn is cancelled with the rest.
func interruptQueuedChatTurns(ctx context.Context, tx pgx.Tx, orgID, sessionID string) error {
	rows, err := tx.Query(ctx, `UPDATE ao_turns turn
		SET state = 'completed', completed_at = now(), updated_at = now()
		WHERE turn.org_id = $1 AND turn.session_id = $2 AND turn.state = 'queued'
		  AND NOT (`+unrunSessionTaskTurnSQL+`)
		RETURNING turn.id, turn.attempt_count`, orgID, sessionID)
	if err != nil {
		return err
	}
	type interruptedTurn struct {
		id      string
		attempt int
	}
	var turns []interruptedTurn
	for rows.Next() {
		var turn interruptedTurn
		if err := rows.Scan(&turn.id, &turn.attempt); err != nil {
			rows.Close()
			return err
		}
		turns = append(turns, turn)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if err := appendTypedEvent(ctx, tx, orgID, sessionID, "chat.turn_interrupted", map[string]any{
			"turnId": turn.id, "attempt": turn.attempt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// unrunSessionTaskTurnSQL is the condition that turn (an ao_turns alias, of
// session $2 in organization $1) is the session's never-claimed task: the turn
// its create queued for the prompt of a session that launches its terminal
// without it.
const unrunSessionTaskTurnSQL = `turn.attempt_count = 0
		  AND EXISTS (
			SELECT 1 FROM ao_events event
			WHERE event.org_id = $1 AND event.session_id = $2
			  AND event.sequence = turn.user_message_sequence AND event.type = 'chat.user_message'
			  AND event.payload ->> '` + sessionTaskEventKey + `' = 'true'
		  )
		  AND EXISTS (
			SELECT 1 FROM ao_sessions session
			WHERE session.org_id = $1 AND session.id = $2 AND session.prompt = ''
		  )`

// GetActiveSessionInterfaceTransition returns the active transition for a
// session, if any, under the caller's tenant context.
func (s *Store) GetActiveSessionInterfaceTransition(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
) (domain.SessionInterfaceTransition, bool, error) {
	return s.getLatestSessionInterfaceTransition(ctx, principal, orgID, sessionID, false)
}

// GetLatestRelevantSessionInterfaceTransition returns the newest attempt only
// when it remains actionable. A completed or cancelled attempt supersedes any
// earlier failure, which stays in audit history but must not reappear in status.
func (s *Store) GetLatestRelevantSessionInterfaceTransition(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
) (domain.SessionInterfaceTransition, bool, error) {
	return s.getLatestSessionInterfaceTransition(ctx, principal, orgID, sessionID, true)
}

func (s *Store) getLatestSessionInterfaceTransition(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID string,
	includeTerminal bool,
) (domain.SessionInterfaceTransition, bool, error) {
	var transition domain.SessionInterfaceTransition
	var found bool
	err := s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		phaseFilter := `t.phase NOT IN ('completed', 'failed', 'cancelled', 'recovery_required')`
		if includeTerminal {
			// Select the newest attempt first. Filtering completed/cancelled rows
			// in SQL would resurrect an older failed notice after a successful
			// switch.
			phaseFilter = `TRUE`
		}
		var sourceInterface string
		err := tx.QueryRow(
			ctx,
			`SELECT t.id, t.org_id, t.session_id, t.source_interface,
				t.target_interface, t.policy, t.phase, t.native_conversation_id,
				t.error_code, t.error_detail, t.notice_acknowledged_at,
				t.created_at, t.updated_at, t.completed_at
			FROM ao_interface_transitions t
			WHERE t.session_id = $1 AND t.org_id = $2
				AND `+phaseFilter+`
			ORDER BY t.created_at DESC, t.id DESC
			LIMIT 1`,
			sessionID,
			orgID,
		).Scan(
			&transition.ID,
			&transition.OrgID,
			&transition.SessionID,
			&sourceInterface,
			&transition.TargetInterface,
			&transition.Policy,
			&transition.Phase,
			&transition.NativeConversationID,
			&transition.ErrorCode,
			&transition.ErrorDetail,
			&transition.NoticeAcknowledgedAt,
			&transition.CreatedAt,
			&transition.UpdatedAt,
			&transition.CompletedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		transition.SourceInterface = domain.SessionInterface(sourceInterface)
		if includeTerminal && !latestTransitionIsRelevant(transition.Phase) {
			return nil
		}
		found = true
		return nil
	})
	if err != nil {
		return domain.SessionInterfaceTransition{}, false, err
	}
	return transition, found, nil
}

func latestTransitionIsRelevant(phase domain.SessionInterfaceTransitionPhase) bool {
	return phase != domain.SessionInterfaceTransitionCompleted &&
		phase != domain.SessionInterfaceTransitionCancelled
}

// ListActiveSessionInterfaceTransitions scans sessions that own an in-progress
// transition. It runs under service context so a reconciler can repair
// transitions left mid-flight by a crashed coordinator across organizations.
func (s *Store) ListActiveSessionInterfaceTransitions(
	ctx context.Context,
) ([]domain.SessionInterfaceTransition, error) {
	var transitions []domain.SessionInterfaceTransition
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`SELECT t.id, t.org_id, t.session_id, t.source_interface,
				t.target_interface, t.policy, t.phase, t.native_conversation_id,
				t.error_code, t.error_detail, t.notice_acknowledged_at,
				t.created_at, t.updated_at, t.completed_at
			FROM ao_interface_transitions t
			WHERE t.phase NOT IN (
				'completed', 'failed', 'cancelled', 'recovery_required'
			)`,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var transition domain.SessionInterfaceTransition
			if err := scanInterfaceTransition(rows, &transition); err != nil {
				return err
			}
			transitions = append(transitions, transition)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return transitions, nil
}

// AdvanceSessionInterfaceTransition moves a transition from one phase to the
// next under the caller's tenant context. It fails closed when the current
// phase no longer matches, so two coordinators cannot advance the same row.
func (s *Store) AdvanceSessionInterfaceTransition(
	ctx context.Context,
	principal domain.Principal,
	orgID, transitionID string,
	from, to domain.SessionInterfaceTransitionPhase,
	nativeConversationID string,
	errorCode, errorDetail string,
) error {
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		var sessionID string
		if err := tx.QueryRow(ctx, `SELECT session_id FROM ao_interface_transitions
			WHERE id = $1 AND org_id = $2 AND phase = $3 FOR UPDATE`,
			transitionID, orgID, from).Scan(&sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransitionStale
			}
			return err
		}
		if interfacehandoff.MayReleaseHeldMessages(from, to) {
			if err := deliverInterfaceTransitionMessages(ctx, tx, orgID, sessionID, transitionID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(
			ctx,
			`UPDATE ao_interface_transitions
			SET phase = $1,
				native_conversation_id = CASE WHEN $2 <> '' THEN $2 ELSE native_conversation_id END,
				error_code = $3,
				error_detail = $4,
				updated_at = now(),
				completed_at = CASE
					WHEN $1 IN ('completed', 'failed', 'cancelled', 'recovery_required')
						THEN now()
					ELSE completed_at
				END
			WHERE id = $5 AND org_id = $6 AND phase = $7`,
			to,
			nativeConversationID,
			errorCode,
			errorDetail,
			transitionID,
			orgID,
			from,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionStale
		}
		return nil
	})
}

// AcknowledgeSessionInterfaceTransitionNotice records that a failed or
// recovered handoff notice has been seen without deleting its audit history.
func (s *Store) AcknowledgeSessionInterfaceTransitionNotice(
	ctx context.Context,
	principal domain.Principal,
	orgID, sessionID, transitionID string,
) error {
	return s.withTenant(ctx, principal, orgID, func(tx pgx.Tx) error {
		transition, err := getInterfaceTransition(ctx, tx, transitionID)
		if err != nil {
			return err
		}
		if transition.SessionID != sessionID {
			return ErrTransitionNotFound
		}
		if transition.Phase != domain.SessionInterfaceTransitionFailed &&
			transition.Phase != domain.SessionInterfaceTransitionRecovery {
			return ErrTransitionNoticeNotAcknowledgeable
		}
		tag, err := tx.Exec(
			ctx,
			`UPDATE ao_interface_transitions
			SET notice_acknowledged_at = now(), updated_at = now()
			WHERE id = $1 AND org_id = $2 AND notice_acknowledged_at IS NULL`,
			transitionID,
			orgID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionNoticeNotAcknowledgeable
		}
		return nil
	})
}

// CompleteCoordinatedInterfaceTransition atomically releases every prompt held
// by an active or recovered handoff and marks the transition complete. The
// transition row is locked first, which pairs with appendUserMessage's lock: a
// message is either included in this delivery or observes the completed
// handoff and routes to the committed controller, never an in-between state.
func (s *Store) CompleteCoordinatedInterfaceTransition(ctx context.Context, owner, transitionID string) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		var orgID, sessionID, phase string
		if err := tx.QueryRow(ctx, `SELECT org_id, session_id, phase
			FROM ao_interface_transitions
			WHERE id = $1 AND claimed_by = $2
			  AND phase IN ('activating', 'recovery_required')
			FOR UPDATE`, transitionID, owner).Scan(&orgID, &sessionID, &phase); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransitionStale
			}
			return err
		}
		// The service claim may span tenants; writes remain explicitly scoped to
		// this transition's tenant before touching tenant-protected turns/events.
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
			return err
		}
		if err := deliverInterfaceTransitionMessages(ctx, tx, orgID, sessionID, transitionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ao_interface_transitions
			SET phase = 'completed', completed_at = now(), updated_at = now()
			WHERE id = $1 AND claimed_by = $2 AND phase = $3`, transitionID, owner, phase); err != nil {
			return err
		}
		return nil
	})
}

// deliverInterfaceTransitionMessages turns every prompt held by one handoff
// into regular worker work. It is called while the transition row is locked
// only after the state table proves a controller is usable. Failed and recovery
// outcomes remain fenced until their adapter has restored a controller.
func deliverInterfaceTransitionMessages(ctx context.Context, tx pgx.Tx, orgID, sessionID, transitionID string) error {
	rows, err := tx.Query(ctx, `SELECT id, turn_id, user_message_sequence, mode_cap, denied_commands
		FROM ao_interface_transition_messages
		WHERE org_id = $1 AND transition_id = $2 AND delivered_at IS NULL
		ORDER BY id FOR UPDATE`, orgID, transitionID)
	if err != nil {
		return err
	}
	// Read every held message before writing: a pgx transaction is one
	// connection, which rejects a statement while a result set is still open
	// ("conn busy").
	type heldMessage struct {
		id       int64
		turnID   string
		sequence int64
		modeCap  string
		denied   []string
	}
	var held []heldMessage
	for rows.Next() {
		var message heldMessage
		if err := rows.Scan(&message.id, &message.turnID, &message.sequence, &message.modeCap, &message.denied); err != nil {
			rows.Close()
			return err
		}
		held = append(held, message)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, message := range held {
		if _, err := tx.Exec(ctx, `INSERT INTO ao_turns (
			id, org_id, session_id, user_message_sequence, mode_cap, denied_commands
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
		ON CONFLICT (id) DO NOTHING`, message.turnID, orgID, sessionID, message.sequence, message.modeCap, nonNilStrings(message.denied)); err != nil {
			return normalizeConstraintError(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE ao_interface_transition_messages SET delivered_at = now() WHERE id = $1`, message.id); err != nil {
			return err
		}
	}
	if len(held) > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_notify('ao_worker_work', $1)`, sessionID); err != nil {
			return err
		}
	}
	return nil
}

// CoordinatedInterfaceTransition is the service-context view the coordinator
// needs: the durable row plus the session interface values it must transition.
type CoordinatedInterfaceTransition struct {
	domain.SessionInterfaceTransition
	Harness string
	// SessionActivityState is the session's provider-reported activity when
	// the transition was claimed. A drain uses it only to decide how long to
	// wait for a source that cannot prove it is idle, never to stop a source.
	SessionActivityState string
	// SessionActivityUnconfirmed reports an "active" state that AO set when it
	// typed a message into the terminal and that no provider hook has
	// confirmed since (activity_source_request_id is still set).
	SessionActivityUnconfirmed bool
	// SessionTerminated reports that the session was deleted (archived) when
	// the transition was claimed: no worker will ever run its handoff steps.
	SessionTerminated bool
	// LiveWorkerEpoch is the epoch of the session's connected worker when a
	// recovery_required transition was claimed, or 0 when none is connected
	// (read for recovery only). A new epoch means a new worker came back, so a
	// recovery waiting out its backoff tries again at once.
	LiveWorkerEpoch int64
}

// ClaimCoordinatedInterfaceTransitions atomically claims one active or
// recovery-required transition row per session for this coordinator owner and
// returns the session context. The partial unique index guarantees a single
// active row per session, so a competing replica can never claim the same
// session concurrently.
func (s *Store) ClaimCoordinatedInterfaceTransitions(
	ctx context.Context,
	owner string,
	limit int,
	lease time.Duration,
) ([]CoordinatedInterfaceTransition, error) {
	var transitions []CoordinatedInterfaceTransition
	err := s.withService(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(
			ctx,
			`WITH candidate AS (
				SELECT t.id, session.harness, session.activity_state,
					session.activity_source_request_id IS NOT NULL AS activity_unconfirmed,
					session.is_terminated
				FROM ao_interface_transitions t
				JOIN ao_sessions session
					ON session.org_id = t.org_id AND session.id = t.session_id
				WHERE t.phase NOT IN ('completed', 'failed', 'cancelled')
					AND (
						t.claimed_by = ''
						OR (t.claimed_by = $1 AND t.claimed_at > now() - $3::interval)
						OR t.claimed_at < now() - $3::interval
					)
				-- A busy drain releases its claim after each inspection. Rotate
				-- it behind transitions that have not yet had a turn.
				ORDER BY COALESCE(t.claimed_at, t.created_at), t.id
				FOR UPDATE OF t SKIP LOCKED
				LIMIT $2
			)
			UPDATE ao_interface_transitions t
			SET claimed_by = $1, claimed_at = now(), updated_at = now()
			FROM candidate
			WHERE t.id = candidate.id
			RETURNING t.id, t.org_id, t.session_id, t.source_interface,
				t.target_interface, t.policy, t.phase, t.native_conversation_id,
				t.selected_model, t.selected_effort,
				t.error_code, t.error_detail, t.notice_acknowledged_at,
				t.created_at, t.updated_at, t.completed_at, candidate.harness,
				candidate.activity_state, candidate.activity_unconfirmed,
				candidate.is_terminated`,
			owner,
			limit,
			lease,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var transition domain.SessionInterfaceTransition
			var harness, activityState string
			var activityUnconfirmed, terminated bool
			if err := rows.Scan(
				&transition.ID,
				&transition.OrgID,
				&transition.SessionID,
				&transition.SourceInterface,
				&transition.TargetInterface,
				&transition.Policy,
				&transition.Phase,
				&transition.NativeConversationID,
				&transition.SelectedModel,
				&transition.SelectedEffort,
				&transition.ErrorCode,
				&transition.ErrorDetail,
				&transition.NoticeAcknowledgedAt,
				&transition.CreatedAt,
				&transition.UpdatedAt,
				&transition.CompletedAt,
				&harness,
				&activityState,
				&activityUnconfirmed,
				&terminated,
			); err != nil {
				return err
			}
			transitions = append(transitions, CoordinatedInterfaceTransition{
				SessionInterfaceTransition: transition,
				Harness:                    harness,
				SessionActivityState:       activityState,
				SessionActivityUnconfirmed: activityUnconfirmed,
				SessionTerminated:          terminated,
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for index := range transitions {
			transition := &transitions[index]
			if transition.Phase != domain.SessionInterfaceTransitionRecovery {
				continue
			}
			// Worker connections are tenant rows: read them in the
			// transition's tenant.
			if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, transition.OrgID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(epoch), 0)
				FROM ao_worker_connections
				WHERE org_id = $1 AND session_id = $2 AND disconnected_at IS NULL`,
				transition.OrgID, transition.SessionID).Scan(&transition.LiveWorkerEpoch); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return transitions, nil
}

// TerminatedInterfaceMessageError is what each prompt held by a handoff of a
// deleted session says: it is shown as a failed turn, so the prompt is neither
// lost silently nor run unasked once the session is restored.
const TerminatedInterfaceMessageError = "This message was not sent because the session was deleted."

// FailTerminatedInterfaceTransition ends a claimed handoff of a deleted
// session as failed with errorCode and errorDetail. Each prompt it held
// becomes a failed turn with TerminatedInterfaceMessageError (a
// chat.turn_aborted event on its message), never a queued one, so a restored
// session shows the prompts as not sent instead of running them late.
func (s *Store) FailTerminatedInterfaceTransition(
	ctx context.Context,
	owner, transitionID string,
	from domain.SessionInterfaceTransitionPhase,
	errorCode, errorDetail string,
) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		var orgID, sessionID string
		if err := tx.QueryRow(ctx, `SELECT org_id, session_id FROM ao_interface_transitions
			WHERE id = $1 AND claimed_by = $2 AND phase = $3 FOR UPDATE`,
			transitionID, owner, from).Scan(&orgID, &sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransitionStale
			}
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, turn_id, user_message_sequence, mode_cap, denied_commands
			FROM ao_interface_transition_messages
			WHERE org_id = $1 AND transition_id = $2 AND delivered_at IS NULL
			ORDER BY id FOR UPDATE`, orgID, transitionID)
		if err != nil {
			return err
		}
		type held struct {
			id       int64
			turnID   string
			sequence int64
			modeCap  string
			denied   []string
		}
		var messages []held
		for rows.Next() {
			var message held
			if err := rows.Scan(&message.id, &message.turnID, &message.sequence, &message.modeCap, &message.denied); err != nil {
				rows.Close()
				return err
			}
			messages = append(messages, message)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, message := range messages {
			tag, err := tx.Exec(ctx, `INSERT INTO ao_turns (
				id, org_id, session_id, user_message_sequence, mode_cap, denied_commands,
				state, error_message, completed_at
			) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, 'failed', $7, now())
			ON CONFLICT (id) DO NOTHING`, message.turnID, orgID, sessionID, message.sequence,
				message.modeCap, nonNilStrings(message.denied), TerminatedInterfaceMessageError)
			if err != nil {
				return normalizeConstraintError(err)
			}
			// Handled: the message never reaches a controller.
			if _, err := tx.Exec(ctx, `UPDATE ao_interface_transition_messages SET delivered_at = now() WHERE id = $1`, message.id); err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			if err := appendTypedEvent(ctx, tx, orgID, sessionID, "chat.turn_aborted", map[string]any{
				"turnId": message.turnID, "attempt": 0, "error": TerminatedInterfaceMessageError,
			}); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE ao_interface_transitions
			SET phase = 'failed', error_code = $3, error_detail = $4,
				completed_at = now(), updated_at = now()
			WHERE id = $1 AND claimed_by = $2 AND phase = $5`,
			transitionID, owner, errorCode, errorDetail, from)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionStale
		}
		return nil
	})
}

// RenewCoordinatedInterfaceClaim keeps a claimed transition leased to this
// owner while a bounded worker operation is in flight.
func (s *Store) RenewCoordinatedInterfaceClaim(
	ctx context.Context,
	owner, transitionID string,
	lease time.Duration,
) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, renewCoordinatedInterfaceClaimSQL, owner, transitionID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionStale
		}
		return nil
	})
}

// AdvanceCoordinatedInterfaceTransition moves a claimed transition to the next
// phase under service context. It fails closed when the current phase no longer
// matches, so two coordinators cannot advance the same row.
func (s *Store) AdvanceCoordinatedInterfaceTransition(
	ctx context.Context,
	owner, transitionID string,
	from, to domain.SessionInterfaceTransitionPhase,
	nativeConversationID string,
	errorCode, errorDetail string,
	releaseHeldMessages bool,
) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		var orgID, sessionID string
		if err := tx.QueryRow(ctx, `SELECT org_id, session_id FROM ao_interface_transitions
			WHERE id = $1 AND claimed_by = $2 AND phase = $3 FOR UPDATE`,
			transitionID, owner, from).Scan(&orgID, &sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransitionStale
			}
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ao.org_id', $1, true)`, orgID); err != nil {
			return err
		}
		if releaseHeldMessages {
			if to != domain.SessionInterfaceTransitionFailed {
				return ErrInvalidTransition
			}
			// The row lock and this delivery share one transaction with the
			// terminal phase update. A source-known-usable failure therefore
			// cannot leave an accepted prompt stranded between controllers.
			if err := deliverInterfaceTransitionMessages(ctx, tx, orgID, sessionID, transitionID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_interface_transitions
			SET phase = $1,
				native_conversation_id = CASE WHEN $2 <> '' THEN $2 ELSE native_conversation_id END,
				error_code = $3,
				error_detail = $4,
				updated_at = now(),
				completed_at = CASE
					WHEN $1 IN ('completed', 'failed', 'cancelled', 'recovery_required')
						THEN now()
					ELSE completed_at
				END
			WHERE id = $5 AND claimed_by = $6 AND phase = $7`,
			to, nativeConversationID, errorCode, errorDetail, transitionID, owner, from)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionStale
		}
		return nil
	})
}

// CommitCoordinatedSessionInterface commits a session's committed interface and
// returns whether the compare-and-swap succeeded. The transition coordinator
// must commit the new controller before releasing the claim so the session row
// never disagrees with the row that explains an in-progress handoff.
func (s *Store) CommitCoordinatedSessionInterface(
	ctx context.Context,
	owner, orgID, transitionID string,
	interfaceValue domain.SessionInterface,
) (bool, error) {
	var committed bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var sessionID string
		err := tx.QueryRow(ctx, commitCoordinatedSessionInterfaceSQL,
			interfaceValue, transitionID, orgID, owner).Scan(&sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransitionStale
		}
		if err != nil {
			return err
		}
		// A drain stops a Chat controller that never claimed the session's
		// first turn without settling it. When the terminal starts with the
		// prompt on its command line (a session created before Chat sessions
		// launched without it) the prompt's turn is settled in the same
		// transaction as the commit, as a start that falls back to the TUI does;
		// otherwise it stays queued for the terminal's turn forwarder.
		if interfaceValue == domain.SessionInterfaceTUI {
			if err := settleUnclaimedPromptTurn(ctx, tx, orgID, sessionID); err != nil {
				return err
			}
		}
		committed = true
		return nil
	})
	return committed, err
}

// RollbackCoordinatedSessionInterface restores the source mode only for a
// claimed target-start failure. Repeating it after a crash is harmless; the
// coordinator still has to prove the source controller is running before it
// marks the handoff failed and releases held messages.
func (s *Store) RollbackCoordinatedSessionInterface(
	ctx context.Context,
	owner, orgID, transitionID string,
) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE ao_sessions AS session
			SET interface = transition.source_interface, updated_at = now()
			FROM ao_interface_transitions AS transition
			WHERE transition.id = $1 AND transition.org_id = $2
			  AND transition.claimed_by = $3
			  AND transition.phase = 'recovery_required'
			  AND transition.error_code = 'TARGET_START_FAILED'
			  AND transition.org_id = session.org_id
			  AND transition.session_id = session.id
			  AND session.interface IN (transition.source_interface, transition.target_interface)`,
			transitionID, orgID, owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrTransitionStale
		}
		return nil
	})
}

// ReleaseCoordinatedInterfaceClaim drops a coordinator's hold on a transition.
func (s *Store) ReleaseCoordinatedInterfaceClaim(
	ctx context.Context,
	owner, transitionID string,
) error {
	return s.withService(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE ao_interface_transitions
				SET claimed_by = '', updated_at = now()
			WHERE id = $1 AND claimed_by = $2`, transitionID, owner)
		return err
	})
}

func insertInterfaceTransition(
	ctx context.Context,
	tx pgx.Tx,
	orgID, sessionID string,
	source, target domain.SessionInterface,
	policy domain.SessionInterfaceTransitionPolicy,
	nativeConversationID, selectedModel, selectedEffort string,
) (domain.SessionInterfaceTransition, error) {
	var transition domain.SessionInterfaceTransition
	err := tx.QueryRow(
		ctx,
		`INSERT INTO ao_interface_transitions (
			org_id, session_id, source_interface, target_interface, policy,
			phase, native_conversation_id, selected_model, selected_effort
		) VALUES ($1, $2, $3, $4, $5, 'requested', $6, $7, $8)
		RETURNING id, org_id, session_id, source_interface, target_interface,
			policy, phase, native_conversation_id, error_code, error_detail,
			notice_acknowledged_at, created_at, updated_at, completed_at`,
		orgID,
		sessionID,
		source,
		target,
		policy,
		nativeConversationID, selectedModel, selectedEffort,
	).Scan(
		&transition.ID,
		&transition.OrgID,
		&transition.SessionID,
		&transition.SourceInterface,
		&transition.TargetInterface,
		&transition.Policy,
		&transition.Phase,
		&transition.NativeConversationID,
		&transition.ErrorCode,
		&transition.ErrorDetail,
		&transition.NoticeAcknowledgedAt,
		&transition.CreatedAt,
		&transition.UpdatedAt,
		&transition.CompletedAt,
	)
	if err != nil {
		return domain.SessionInterfaceTransition{}, normalizeConstraintError(err)
	}
	transition.SelectedModel = selectedModel
	transition.SelectedEffort = selectedEffort
	return transition, nil
}

func getInterfaceTransition(
	ctx context.Context,
	tx pgx.Tx,
	transitionID string,
) (domain.SessionInterfaceTransition, error) {
	var transition domain.SessionInterfaceTransition
	err := tx.QueryRow(
		ctx,
		`SELECT id, org_id, session_id, source_interface, target_interface,
			policy, phase, native_conversation_id, error_code, error_detail,
			notice_acknowledged_at, created_at, updated_at, completed_at
		FROM ao_interface_transitions
		WHERE id = $1`,
		transitionID,
	).Scan(
		&transition.ID,
		&transition.OrgID,
		&transition.SessionID,
		&transition.SourceInterface,
		&transition.TargetInterface,
		&transition.Policy,
		&transition.Phase,
		&transition.NativeConversationID,
		&transition.ErrorCode,
		&transition.ErrorDetail,
		&transition.NoticeAcknowledgedAt,
		&transition.CreatedAt,
		&transition.UpdatedAt,
		&transition.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SessionInterfaceTransition{}, ErrTransitionNotFound
	}
	if err != nil {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("load interface transition: %w", err)
	}
	return transition, nil
}

func scanInterfaceTransition(row pgx.Row, transition *domain.SessionInterfaceTransition) error {
	return row.Scan(
		&transition.ID,
		&transition.OrgID,
		&transition.SessionID,
		&transition.SourceInterface,
		&transition.TargetInterface,
		&transition.Policy,
		&transition.Phase,
		&transition.NativeConversationID,
		&transition.ErrorCode,
		&transition.ErrorDetail,
		&transition.NoticeAcknowledgedAt,
		&transition.CreatedAt,
		&transition.UpdatedAt,
		&transition.CompletedAt,
	)
}
