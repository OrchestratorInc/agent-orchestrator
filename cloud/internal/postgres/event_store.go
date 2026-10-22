package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/jackc/pgx/v5"
)

var clientEventTypes = []string{
	"agent.activity",
	"agent.ready",
	"worker.connected",
	"worker.ready",
	"sandbox.provisioning",
	"workspace.changed",
	"workspace.setup",
	"workspace.script.step",
	"pull_request.created",
	"pull_request.claimed",
	"review.submitted",
	"scm.updated",
	"chat.user_message",
	"chat.assistant_delta",
	"chat.activity",
	"chat.turn_started",
	"chat.turn_completed",
	"chat.turn_interrupted",
	"chat.turn_aborted",
	"chat.interrupt_requested",
	"chat.turn_steered",
	"chat.turn_steer_requested",
	"chat.turn_steer_failed",
	"chat.approval_requested",
	"chat.approval_decided",
	"chat.turn_capabilities",
}

type chatMessagePayload struct {
	Text string `json:"text"`
	domain.ChatTurnSettings
}

func (s *Store) SendMessage(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	sessionID string,
	idempotencyKey string,
	text string,
	settings domain.ChatTurnSettings,
) (domain.ClientEvent, error) {
	var event domain.ClientEvent
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		if access.Role == "viewer" {
			return ErrForbidden
		}
		// A message wakes a paused session, which a billed session may do
		// only within its plan: refuse it here so the sender sees why.
		wake, err := s.billing.forAccess(principal, access, orgID).replayOrMessageWakeTx(ctx, tx, orgID, sessionID, idempotencyKey, true)
		if err != nil {
			return err
		}
		event, err = sendMessageTx(
			ctx, tx, orgID, sessionID, idempotencyKey, text, principal.UserID, "",
			access.ModeCap, access.DeniedCommands, settings, wake,
		)
		return err
	})
	return event, err
}

func sendMessageTx(
	ctx context.Context,
	tx pgx.Tx,
	orgID, sessionID, idempotencyKey, text, actorUserID, actorSessionID string,
	modeCap string,
	deniedCommands []string,
	settings domain.ChatTurnSettings,
	// wake lets the message wake a paused sandbox. Billed sessions decide it
	// with messageWakeTx, at the start of the transaction (it may take the
	// organization lock); without it a paused session keeps the message
	// queued until it is woken.
	wake bool,
) (domain.ClientEvent, error) {
	payload, err := json.Marshal(chatMessagePayload{Text: text, ChatTurnSettings: settings})
	if err != nil {
		return domain.ClientEvent{}, err
	}
	var commandID string
	err = tx.QueryRow(
		ctx,
		`INSERT INTO ao_commands (
			org_id, session_id, idempotency_key, kind, payload
		) VALUES ($1, $2, $3, 'message.send', $4)
		ON CONFLICT (org_id, idempotency_key) DO NOTHING
		RETURNING id`,
		orgID, sessionID, idempotencyKey, payload,
	).Scan(&commandID)
	if errors.Is(err, pgx.ErrNoRows) {
		var event domain.ClientEvent
		err := loadIdempotentMessage(
			ctx, tx, orgID, sessionID, idempotencyKey, payload, &event,
		)
		return event, err
	}
	if err != nil {
		return domain.ClientEvent{}, normalizeConstraintError(err)
	}
	event, err := appendUserMessage(ctx, tx, orgID, sessionID, idempotencyKey, text, modeCap, deniedCommands, settings)
	if err != nil {
		return domain.ClientEvent{}, err
	}
	// A user message is proof of life: wake a sandbox the idle-pause scanner
	// paused for silence and schedule reconciliation immediately even when the
	// sandbox was already running, so provider deadline extension cannot wait
	// for the next ordinary observation. This never overrides a desired
	// 'stopped' or 'deleted' set explicitly elsewhere.
	if _, err := tx.Exec(
		ctx,
		`UPDATE ao_sandboxes
		SET desired_state = 'running',
			startup_started_at = CASE
				WHEN desired_state = 'paused' THEN now()
				ELSE startup_started_at
			END,
			reconcile_after = now(), updated_at = now()
		WHERE session_id = $1 AND org_id = $2
			AND (desired_state = 'running' OR ($3 AND desired_state = 'paused'))`,
		sessionID, orgID, wake,
	); err != nil {
		return domain.ClientEvent{}, err
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE ao_commands
		SET status = 'succeeded',
			result = jsonb_build_object('eventSequence', $1::bigint),
			updated_at = now()
		WHERE id = $2`,
		event.Sequence, commandID,
	); err != nil {
		return domain.ClientEvent{}, err
	}
	auditSQL := `INSERT INTO ao_audit_events (
		org_id, actor_user_id, action, resource_type, resource_id, metadata
	) VALUES (
		$1, $2, 'session.message_queued', 'session', $3,
		jsonb_build_object('sequence', $4::bigint)
	)`
	auditArgs := []any{orgID, actorUserID, sessionID, event.Sequence}
	if actorSessionID != "" {
		auditSQL = `INSERT INTO ao_audit_events (
			org_id, action, resource_type, resource_id, metadata
		) VALUES (
			$1, 'session.message_queued', 'session', $2,
			jsonb_build_object(
				'sequence', $3::bigint,
				'actorSessionId', $4::text
			)
		)`
		auditArgs = []any{orgID, sessionID, event.Sequence, actorSessionID}
	}
	_, err = tx.Exec(ctx, auditSQL, auditArgs...)
	return event, err
}

func loadIdempotentMessage(
	ctx context.Context,
	tx pgx.Tx,
	orgID string,
	sessionID string,
	idempotencyKey string,
	payload []byte,
	event *domain.ClientEvent,
) error {
	var storedPayload []byte
	var storedSessionID, kind, status string
	var sequence int64
	err := tx.QueryRow(
		ctx,
		`SELECT session_id, kind, status, payload,
			(result->>'eventSequence')::bigint
		FROM ao_commands
		WHERE org_id = $1 AND idempotency_key = $2`,
		orgID,
		idempotencyKey,
	).Scan(&storedSessionID, &kind, &status, &storedPayload, &sequence)
	if err != nil {
		return err
	}
	if storedSessionID != sessionID ||
		kind != "message.send" ||
		status != "succeeded" ||
		!jsonEqual(storedPayload, payload) {
		return ErrIdempotencyMismatch
	}
	return scanClientEvent(tx.QueryRow(
		ctx,
		`SELECT session_id, sequence, type, payload, created_at
		FROM ao_events
		WHERE org_id = $1 AND session_id = $2 AND sequence = $3`,
		orgID,
		sessionID,
		sequence,
	), event)
}

// WorkerAgentStarted reports whether the session's worker epoch (workerID)
// started its coding agent: it reported agent.ready (a terminal agent), or it
// claimed a turn (a Chat session's agent runs only for a claimed turn, and a
// session that stays in Chat never sends agent.ready). A worker claims no turn
// while its Setup runs or after it failed. Its agent.ready is the session's
// newest one, as a superseded epoch can report nothing, so only the newest is
// read. One round trip on the session's turn and event indexes.
func (s *Store) WorkerAgentStarted(ctx context.Context, orgID, sessionID, workerID string, epoch int64) (bool, error) {
	var started bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT EXISTS (
					SELECT 1 FROM ao_turns
					WHERE org_id = $1 AND session_id = $2 AND worker_epoch = $4 AND attempt_count > 0
				) OR COALESCE((
					SELECT payload->>'workerId' = $3 AND payload->>'epoch' = $4::bigint::text
					FROM ao_events
					WHERE org_id = $1 AND session_id = $2 AND type = 'agent.ready'
					ORDER BY sequence DESC
					LIMIT 1
				), false)`,
			orgID, sessionID, workerID, epoch,
		).Scan(&started)
	})
	return started, err
}

// AppendSessionEvent records a control-plane or worker event on a session
// stream. Unlike a user message it does not open a turn, and it is allowed on a
// terminated session so lifecycle history stays complete.
func (s *Store) AppendSessionEvent(
	ctx context.Context,
	orgID string,
	sessionID string,
	eventType string,
	payload json.RawMessage,
) (domain.ClientEvent, error) {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var event domain.ClientEvent
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var sequence int64
		err := tx.QueryRow(
			ctx,
			`UPDATE ao_sessions
			SET next_sequence = next_sequence + 1,
				updated_at = now()
			WHERE org_id = $1 AND id = $2
			RETURNING next_sequence - 1`,
			orgID,
			sessionID,
		).Scan(&sequence)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := scanClientEvent(tx.QueryRow(
			ctx,
			`INSERT INTO ao_events (
				org_id, session_id, sequence, type, payload
			) VALUES ($1, $2, $3, $4, $5)
			RETURNING session_id, sequence, type, payload, created_at`,
			orgID,
			sessionID,
			sequence,
			eventType,
			payload,
		), &event); err != nil {
			return err
		}
		return dropSupersededScriptUpdatesTx(ctx, tx, orgID, event)
	})
	if err != nil {
		return domain.ClientEvent{}, err
	}
	return event, nil
}

// AppendInteractiveConversationFacts projects native TUI hooks into the durable
// Chat transcript. Headless Chat hooks must not be projected twice.
func (s *Store) AppendInteractiveConversationFacts(ctx context.Context, orgID, sessionID, eventType, sourceInterface, userPrompt, assistantUpdate string) error {
	if sourceInterface != "" && sourceInterface != "tui" {
		return nil
	}
	if eventType != "user-prompt-submit" && eventType != "stop" {
		return nil
	}
	text := userPrompt
	eventTypeOut := "chat.user_message"
	if eventType == "stop" {
		text = assistantUpdate
		eventTypeOut = "chat.assistant_delta"
	}
	if text == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if sourceInterface == "" {
			var current string
			if err := tx.QueryRow(ctx, `SELECT interface FROM ao_sessions WHERE org_id = $1 AND id = $2 AND is_terminated = false`, orgID, sessionID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			} else if err != nil {
				return err
			} else if current != "tui" {
				return nil
			}
		}
		// The initial prompt is already recorded as a chat.user_message at session
		// create (createSessionTx), and the worker auto-runs it in the TUI, whose
		// user-prompt-submit hook mirrors the identical text back here — producing a
		// second, duplicate prompt bubble in ChatUI. Skip a user-prompt mirror that
		// only repeats the latest recorded user message while it is still
		// unanswered; a genuinely distinct TUI prompt (the reason this mirror
		// exists) and a real re-submit of the same text after the agent has replied
		// (an intervening chat.assistant_delta) both still record. Assistant deltas
		// are never deduped.
		if eventTypeOut == "chat.user_message" {
			var lastText string
			var answered bool
			switch err := tx.QueryRow(ctx, `
				WITH last_user AS (
					SELECT sequence, payload->>'text' AS text
					FROM ao_events
					WHERE org_id = $1 AND session_id = $2 AND type = 'chat.user_message'
					ORDER BY sequence DESC LIMIT 1
				)
				SELECT lu.text, EXISTS (
					SELECT 1 FROM ao_events e
					WHERE e.org_id = $1 AND e.session_id = $2
					  AND e.type = 'chat.assistant_delta' AND e.sequence > lu.sequence
				)
				FROM last_user lu`, orgID, sessionID).Scan(&lastText, &answered); {
			case errors.Is(err, pgx.ErrNoRows):
				// No prior user message recorded — this is the first, record it.
			case err != nil:
				return err
			case lastText == text && !answered:
				return nil // duplicate of the still-unanswered latest prompt
			}
		}
		var sequence int64
		if err := tx.QueryRow(ctx, `UPDATE ao_sessions SET next_sequence = next_sequence + 1, updated_at = now()
			WHERE org_id = $1 AND id = $2 AND is_terminated = false RETURNING next_sequence - 1`, orgID, sessionID).Scan(&sequence); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO ao_events (org_id, session_id, sequence, type, payload)
			VALUES ($1, $2, $3, $4, $5)`, orgID, sessionID, sequence, eventTypeOut, payload)
		return err
	})
}

// appendUserMessage records a user message and, depending on how the
// session is currently running, either injects it directly into an
// already-open interactive agent terminal or queues it as a new turn.
// modeCap/deniedCommands are the sender's share-grant cap, if any (zero
// values for an org member or an unshared session) — a live terminal was
// opened under whatever mode was in force when it started, so a capped
// sender's message is re-checked against that same cap here rather than
// trusted just because the terminal already exists; a queued turn instead
// carries the cap forward onto the ao_turns row for ClaimWorkerTurn to
// apply against the session's mode at claim time.
func appendUserMessage(
	ctx context.Context,
	tx pgx.Tx,
	orgID string,
	sessionID string,
	clientMessageID string,
	text string,
	modeCap string,
	deniedCommands []string,
	settings domain.ChatTurnSettings,
) (domain.ClientEvent, error) {
	event, err := appendUserMessageEvent(ctx, tx, orgID, sessionID, text, settings)
	if err != nil {
		return domain.ClientEvent{}, err
	}
	// The transition row serializes message acceptance with the coordinator's
	// completion, preventing delivery to a controller that is being replaced.
	var transitionID string
	transitionErr := tx.QueryRow(ctx, `SELECT id FROM ao_interface_transitions
		WHERE org_id = $1 AND session_id = $2
		  AND phase NOT IN ('completed', 'failed', 'cancelled')
		FOR UPDATE`, orgID, sessionID).Scan(&transitionID)
	if transitionErr == nil {
		var turnID string
		if err := tx.QueryRow(ctx, `INSERT INTO ao_interface_transition_messages (
			org_id, transition_id, client_message_id, message, user_message_sequence,
			mode_cap, denied_commands
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING turn_id`, orgID, transitionID, clientMessageID, text, event.Sequence,
			modeCap, nonNilStrings(deniedCommands)).Scan(&turnID); err != nil {
			return domain.ClientEvent{}, normalizeConstraintError(err)
		}
		if err := attachTurnID(ctx, tx, orgID, sessionID, event.Sequence, turnID, &event); err != nil {
			return domain.ClientEvent{}, err
		}
		return event, nil
	}
	if !errors.Is(transitionErr, pgx.ErrNoRows) {
		return domain.ClientEvent{}, transitionErr
	}
	var terminalID string
	var workerEpoch int64
	var sessionMode string
	var sessionDeniedCommands []string
	// The direct PTY fast path only applies while the agent is at its prompt:
	// text typed into a mid-turn harness lands in the composer unsubmitted and
	// the agent never sees it (a report the orchestrator is actively polling
	// for would deadlock it). A busy agent's message queues durably below and
	// is delivered by the worker once the turn ends.
	err = tx.QueryRow(ctx,
		`SELECT terminal.id, terminal.worker_epoch, session.mode, session.denied_commands
		FROM ao_terminal_sessions terminal
		JOIN ao_sessions session
			ON session.org_id = terminal.org_id AND session.id = terminal.session_id
		WHERE terminal.org_id = $1 AND terminal.session_id = $2 AND terminal.kind = 'agent' AND NOT EXISTS (
				SELECT 1 FROM ao_review_runs review_terminal_run
				WHERE review_terminal_run.org_id = terminal.org_id
				  AND review_terminal_run.review_terminal_id = terminal.id
			)
		  AND session.interface = 'tui'
		  AND terminal.state = 'open' AND terminal.expires_at > now()
		  AND session.activity_state <> 'active'
		ORDER BY terminal.created_at DESC
		LIMIT 1`,
		orgID, sessionID,
	).Scan(&terminalID, &workerEpoch, &sessionMode, &sessionDeniedCommands)
	if err == nil {
		effective := effectiveMode(sessionMode, modeCap)
		effectiveDenied := effectiveDeniedCommands(sessionDeniedCommands, deniedCommands)
		if effective == "read-only" || len(effectiveDenied) != 0 {
			return domain.ClientEvent{}, ErrForbidden
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"terminalId": terminalID,
			"data":       worker.EncodeTerminalInput(text),
		})
		if marshalErr != nil {
			return domain.ClientEvent{}, marshalErr
		}
		var inputRequestID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO ao_worker_requests (
				org_id, session_id, worker_epoch, kind, payload, expires_at
			) VALUES ($1, $2, $3, 'terminal.input', $4, now() + interval '60 seconds')
			RETURNING id`,
			orgID, sessionID, workerEpoch, payload,
		).Scan(&inputRequestID); err != nil {
			return domain.ClientEvent{}, err
		}
		// A live agent terminal does not create an ao_turn. Mark the session
		// active at enqueue time so status/sidebar projections cannot claim it
		// is idle while the worker still has ordered terminal input pending.
		if _, err := tx.Exec(ctx,
			`UPDATE ao_sessions
			SET activity_state = 'active', activity_source_request_id = $3,
				updated_at = now()
			WHERE org_id = $1 AND id = $2 AND is_terminated = false`,
			orgID, sessionID, inputRequestID,
		); err != nil {
			return domain.ClientEvent{}, err
		}
		// Wake a worker blocked in WaitForWork so it claims this terminal.input
		// request without busy-polling. Delivered on commit; the durable queue
		// stays authoritative.
		if _, err := tx.Exec(ctx, `SELECT pg_notify('ao_worker_work', $1)`, sessionID); err != nil {
			return domain.ClientEvent{}, err
		}
		return event, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ClientEvent{}, err
	}
	var turnID string
	if err := tx.QueryRow(
		ctx,
		`INSERT INTO ao_turns (
			org_id, session_id, user_message_sequence, mode_cap, denied_commands
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5) RETURNING id`,
		orgID,
		sessionID,
		event.Sequence,
		modeCap,
		nonNilStrings(deniedCommands),
	).Scan(&turnID); err != nil {
		return domain.ClientEvent{}, normalizeConstraintError(err)
	}
	if err := attachTurnID(ctx, tx, orgID, sessionID, event.Sequence, turnID, &event); err != nil {
		return domain.ClientEvent{}, err
	}
	// Wake a worker blocked in WaitForWork so it claims this queued turn without
	// busy-polling. Delivered on commit; the durable ao_turns row stays the
	// source of truth if the notification is ever lost.
	if _, err := tx.Exec(ctx, `SELECT pg_notify('ao_worker_work', $1)`, sessionID); err != nil {
		return domain.ClientEvent{}, err
	}
	return event, nil
}

// sessionTaskEventKey marks, in its chat.user_message payload, the prompt a
// session created in Chat queued as its first turn: the session's task, which
// its terminal launch does not carry. A stop-now switch to the terminal keeps
// that turn queued for the terminal's forwarder (interruptQueuedChatTurns)
// while it cancels the messages sent after it.
const sessionTaskEventKey = "sessionTask"

// queueInitialChatTurnTx queues a new Chat session's prompt (its first
// chat.user_message event) as the session's first turn, as a message sent to
// an idle Chat session is, marked as the session's task. No worker exists
// yet, so nobody is woken.
func queueInitialChatTurnTx(ctx context.Context, tx pgx.Tx, orgID, sessionID string, event domain.ClientEvent) error {
	var turnID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO ao_turns (org_id, session_id, user_message_sequence)
		VALUES ($1, $2, $3) RETURNING id`,
		orgID, sessionID, event.Sequence,
	).Scan(&turnID); err != nil {
		return normalizeConstraintError(err)
	}
	if err := attachTurnID(ctx, tx, orgID, sessionID, event.Sequence, turnID, &event); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE ao_events SET payload = payload || jsonb_build_object($4::text, true)
		WHERE org_id = $1 AND session_id = $2 AND sequence = $3 AND type = 'chat.user_message'`,
		orgID, sessionID, event.Sequence, sessionTaskEventKey)
	return err
}

func attachTurnID(ctx context.Context, tx pgx.Tx, orgID, sessionID string, sequence int64, turnID string, event *domain.ClientEvent) error {
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	payload["turnId"] = turnID
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ao_events SET payload = $4
		WHERE org_id = $1 AND session_id = $2 AND sequence = $3 AND type = 'chat.user_message'`,
		orgID, sessionID, sequence, raw); err != nil {
		return err
	}
	event.Payload = raw
	return nil
}

func appendUserMessageEvent(
	ctx context.Context,
	tx pgx.Tx,
	orgID string,
	sessionID string,
	text string,
	selected ...domain.ChatTurnSettings,
) (domain.ClientEvent, error) {
	var sequence int64
	err := tx.QueryRow(
		ctx,
		`UPDATE ao_sessions
		SET next_sequence = next_sequence + 1, updated_at = now(), last_user_message_at = now()
		WHERE org_id = $1 AND id = $2 AND is_terminated = false
		RETURNING next_sequence - 1`,
		orgID,
		sessionID,
	).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		var terminated bool
		if err := tx.QueryRow(
			ctx,
			`SELECT is_terminated
			FROM ao_sessions
			WHERE org_id = $1 AND id = $2`,
			orgID,
			sessionID,
		).Scan(&terminated); errors.Is(err, pgx.ErrNoRows) {
			return domain.ClientEvent{}, ErrNotFound
		} else if err != nil {
			return domain.ClientEvent{}, err
		}
		if terminated {
			return domain.ClientEvent{}, ErrConflict
		}
	}
	if err != nil {
		return domain.ClientEvent{}, err
	}

	settings := domain.ChatTurnSettings{}
	if len(selected) > 0 {
		settings = selected[0]
	}
	payload, err := json.Marshal(chatMessagePayload{Text: text, ChatTurnSettings: settings})
	if err != nil {
		return domain.ClientEvent{}, err
	}
	var event domain.ClientEvent
	err = scanClientEvent(tx.QueryRow(
		ctx,
		`INSERT INTO ao_events (
			org_id, session_id, sequence, type, payload
		) VALUES ($1, $2, $3, 'chat.user_message', $4)
		RETURNING session_id, sequence, type, payload, created_at`,
		orgID,
		sessionID,
		sequence,
		payload,
	), &event)
	if err != nil {
		return domain.ClientEvent{}, err
	}
	return event, nil
}

// ListClientEvents lists up to limit of the session's client events after a
// sequence, and whether more follow. With types, only those of the client
// event types it names. A principal outside the session's organization gets
// Setup and Cleanup events without their output (hideScriptOutput).
func (s *Store) ListClientEvents(
	ctx context.Context,
	principal domain.Principal,
	orgID string,
	sessionID string,
	after int64,
	limit int,
	types []string,
) ([]domain.ClientEvent, bool, error) {
	served := clientEventTypes
	if len(types) > 0 {
		served = nil
		for _, eventType := range clientEventTypes {
			if slices.Contains(types, eventType) {
				served = append(served, eventType)
			}
		}
		if len(served) == 0 {
			// Only types not served to clients: nothing matches.
			served = []string{}
		}
	}
	var events []domain.ClientEvent
	member := false
	err := s.withSessionAccess(ctx, principal, orgID, sessionID, func(tx pgx.Tx, access sessionAccess) error {
		member = access.Role == "member"
		var exists bool
		if err := tx.QueryRow(
			ctx,
			`SELECT EXISTS (
				SELECT 1 FROM ao_sessions WHERE org_id = $1 AND id = $2
			)`,
			orgID,
			sessionID,
		).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		rows, err := tx.Query(
			ctx,
			`SELECT session_id, sequence, type, payload, created_at
			FROM ao_events
			WHERE org_id = $1
			  AND session_id = $2
			  AND sequence > $3
			  AND type = ANY($4)
			ORDER BY sequence
			LIMIT $5`,
			orgID,
			sessionID,
			after,
			served,
			limit+1,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event domain.ClientEvent
			if err := scanClientEvent(rows, &event); err != nil {
				return err
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	if !member {
		for index := range events {
			hideScriptOutput(&events[index])
		}
	}
	return events, hasMore, nil
}

// dropSupersededScriptUpdatesTx deletes a Setup step's live updates (its
// running events) once its final event is appended: the final event carries
// everything they did, and a chatty step sends one every few seconds. Clients
// keep the newest event per (runId, step), and read by sequence after a
// cursor, which deleting older rows never moves; sequences come from
// ao_sessions.next_sequence, so a gap is never reused. The app role may
// delete ao_events rows of the organization in context (tenant policy, and
// DELETE is granted on every table). It runs once per finished Setup step,
// and Setup runs when a session starts or reopens.
func dropSupersededScriptUpdatesTx(ctx context.Context, tx pgx.Tx, orgID string, event domain.ClientEvent) error {
	if event.Type != worker.ScriptStepEventType {
		return nil
	}
	var step struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(event.Payload, &step); err != nil || step.Status == worker.ScriptStepRunning {
		return nil
	}
	_, err := tx.Exec(ctx,
		`DELETE FROM ao_events
		WHERE org_id = $1
		  AND session_id = $2
		  AND sequence < $3
		  AND type = $4
		  AND payload->>'status' = $5
		  AND payload->>'runId' = $6::jsonb->>'runId'
		  AND payload->'step' = $6::jsonb->'step'`,
		orgID, event.SessionID, event.Sequence, worker.ScriptStepEventType, worker.ScriptStepRunning, event.Payload)
	return err
}

// scriptOutputFields are what a Setup or Cleanup event (workspace.setup,
// workspace.script.step) tells of the sandbox beyond the step itself: the
// commands' output, and the user, groups, directory and log file it ran with
// (and the hint, which names the user). Only the organization's members see
// them; output is redacted, but a project's scripts can print anything.
var scriptOutputFields = []string{"output", "user", "groups", "dir", "logPath", "hint"}

// hideScriptOutput strips scriptOutputFields from a Setup or Cleanup event
// served to someone outside the organization (a share-grant holder) and marks
// it outputHidden. A workspace.script.step event keeps an empty output, which
// its contract requires. An event that cannot be read is served without its
// payload.
func hideScriptOutput(event *domain.ClientEvent) {
	if event.Type != worker.ScriptStepEventType && event.Type != worker.SetupEventType {
		return
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		event.Payload = json.RawMessage(`{"outputHidden":true}`)
		return
	}
	for _, field := range scriptOutputFields {
		delete(payload, field)
	}
	if event.Type == worker.ScriptStepEventType {
		payload["output"] = json.RawMessage(`""`)
		payload["outputTruncated"] = json.RawMessage(`false`)
	}
	payload["outputHidden"] = json.RawMessage(`true`)
	if encoded, err := json.Marshal(payload); err == nil {
		event.Payload = encoded
	}
}

func scanClientEvent(row scanner, event *domain.ClientEvent) error {
	return row.Scan(
		&event.SessionID,
		&event.Sequence,
		&event.Type,
		&event.Payload,
		&event.CreatedAt,
	)
}
