package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// FallBackChatStart moves a session created in Chat to the TUI before its
// first Chat controller starts, because the worker that is about to run it
// cannot start in Chat. Only a session whose Chat came from its create (no
// interface transition was ever recorded) and that never started moves: a
// session the person switched to Chat stays on the interface they chose, and
// a session whose Chat already ran (a turn was claimed or settled, or the
// agent reported its conversation) stays in Chat on a later worker restart
// or reconnect, rather than showing a false notice and running its first
// task again in the terminal.
//
// In the same transaction the prompt's queued first turn is settled when the
// TUI agent receives the prompt on its command line (settleUnclaimedPromptTurn;
// a session created in Chat launches without it, and its turn stays for the
// terminal's forwarder), and a failed
// TUI-to-Chat transition carrying code and detail is recorded, which the
// desktop shows as the session's interface notice. It reports whether the
// session moved.
func (s *Store) FallBackChatStart(ctx context.Context, orgID, sessionID, code, detail string) (bool, error) {
	var moved bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		moved, err = fallBackChatStartTx(ctx, tx, orgID, sessionID, code, detail)
		return err
	})
	return moved, err
}

// WorkerFallBackChatStart is FallBackChatStart asked for by the session's
// current worker, which found it cannot start the Chat controller (for
// Claude, no ACP adapter and no npm to install one).
func (s *Store) WorkerFallBackChatStart(
	ctx context.Context,
	orgID, sessionID, workerID string,
	epoch int64,
	code, detail string,
) (bool, error) {
	var moved bool
	err := s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := requireCurrentWorker(ctx, tx, orgID, sessionID, workerID, epoch); err != nil {
			return err
		}
		var err error
		moved, err = fallBackChatStartTx(ctx, tx, orgID, sessionID, code, detail)
		return err
	})
	return moved, err
}

func fallBackChatStartTx(ctx context.Context, tx pgx.Tx, orgID, sessionID, code, detail string) (bool, error) {
	// Lock the session row first, as StartSessionInterfaceTransition does, and
	// check the session's history in later statements: their snapshot is taken
	// after the lock, so a transition committed while this waited is seen
	// rather than missed by a snapshot taken before it.
	var iface, prompt, agentSessionID string
	err := tx.QueryRow(ctx,
		`SELECT interface, prompt, agent_session_id FROM ao_sessions
		WHERE org_id = $1 AND id = $2
		FOR UPDATE`,
		orgID, sessionID,
	).Scan(&iface, &prompt, &agentSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if iface != "chat" || agentSessionID != "" {
		return false, nil
	}
	var started bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM ao_interface_transitions transition
			WHERE transition.org_id = $1 AND transition.session_id = $2
		) OR EXISTS (
			SELECT 1 FROM ao_turns turn
			WHERE turn.org_id = $1 AND turn.session_id = $2
			  AND (turn.attempt_count > 0 OR turn.state <> 'queued')
		)`,
		orgID, sessionID,
	).Scan(&started); err != nil {
		return false, err
	}
	// Any transition, active or settled, keeps the session where it is: an
	// active one is the person's switch in progress.
	if started {
		return false, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE ao_sessions SET interface = 'tui', updated_at = now()
		WHERE org_id = $1 AND id = $2`,
		orgID, sessionID,
	); err != nil {
		return false, err
	}
	if prompt != "" {
		if err := settleUnclaimedPromptTurn(ctx, tx, orgID, sessionID); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO ao_interface_transitions (
			org_id, session_id, source_interface, target_interface, policy,
			phase, error_code, error_detail, completed_at
		) VALUES ($1, $2, 'tui', 'chat', 'interrupt', 'failed', $3, $4, now())`,
		orgID, sessionID, code, detail,
	); err != nil {
		return false, normalizeConstraintError(err)
	}
	return true, nil
}

// settleUnclaimedPromptTurn settles the turn a session created in Chat queued
// for its prompt, while no Chat controller has claimed any turn, as the
// session moves to the TUI, only when its terminal launch carries the prompt
// (ao_sessions.prompt, kept by a control plane that predates Chat sessions
// launching without it): that agent gets the task on its command line, and
// left queued the terminal's turn forwarder would type it again. A session
// created in Chat now launches without the prompt, so nothing is settled and
// the forwarder delivers the turn, once, after the terminal agent starts.
func settleUnclaimedPromptTurn(ctx context.Context, tx pgx.Tx, orgID, sessionID string) error {
	// The create records the prompt as the session's first user message.
	_, err := tx.Exec(ctx,
		`UPDATE ao_turns turn
		SET state = 'completed', completed_at = now(), updated_at = now()
		WHERE turn.org_id = $1 AND turn.session_id = $2
		  AND turn.state = 'queued' AND turn.attempt_count = 0
		  AND EXISTS (
			SELECT 1 FROM ao_sessions session
			WHERE session.org_id = $1 AND session.id = $2 AND session.prompt <> ''
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM ao_turns claimed
			WHERE claimed.org_id = $1 AND claimed.session_id = $2 AND claimed.attempt_count > 0
		  )
		  AND turn.user_message_sequence = (
			SELECT min(event.sequence) FROM ao_events event
			WHERE event.org_id = $1 AND event.session_id = $2
			  AND event.type = 'chat.user_message'
		  )`,
		orgID, sessionID,
	)
	return err
}
