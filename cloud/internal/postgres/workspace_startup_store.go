package postgres

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// maxWorkspaceStartupMessage bounds the user-facing message a worker reports.
const maxWorkspaceStartupMessage = 1024

// RecordWorkspaceStartupError stores a startup error the session's current
// worker reported because it could not prepare the workspace (see
// domain.WorkspaceStartupErrorCodes). Unlike a reconciler startup error it
// survives the worker's heartbeats, so the desktop keeps showing it until the
// worker reports the workspace ready. The worker reports a failure once per
// startup attempt, so each report is a new failure and moves startup_error_at
// even when it repeats the last one: a retried Setup that fails the same way
// again is told apart from the failure it retried. (The reconciler's
// RecordSandboxStartupError keeps its time while the same error repeats, as
// it records the same ongoing condition on every pass.)
func (s *Store) RecordWorkspaceStartupError(
	ctx context.Context,
	orgID, sessionID, workerID string,
	epoch int64,
	code, message, detail string,
) error {
	if !domain.IsWorkspaceStartupErrorCode(code) {
		return fmt.Errorf("%w: unknown workspace startup error code", ErrInvalid)
	}
	message = truncateTail(strings.TrimSpace(message), maxWorkspaceStartupMessage, false)
	detail = truncateTail(detail, maxStartupErrorDetail, true)
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		current, err := workerConnectionCurrent(ctx, tx, orgID, sessionID, workerID, epoch)
		if err != nil {
			return err
		}
		if !current {
			return ErrStaleWorker
		}
		tag, err := tx.Exec(ctx,
			`UPDATE ao_sandboxes
			SET startup_error_code = $3,
				startup_error_message = $4,
				startup_error_detail = $5,
				startup_error_at = now(),
				updated_at = now()
			WHERE session_id = $1 AND org_id = $2 AND desired_state = 'running'`,
			sessionID, orgID, code, message, detail,
		)
		if err != nil {
			return fmt.Errorf("record workspace startup error: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ClearWorkspaceStartupError removes a startup error the worker reported once
// the workspace is ready. A startup error recorded by the reconciler is left
// to the heartbeat that already clears it.
func (s *Store) ClearWorkspaceStartupError(
	ctx context.Context,
	orgID, sessionID, workerID string,
	epoch int64,
) error {
	return s.withOrg(ctx, orgID, func(tx pgx.Tx) error {
		current, err := workerConnectionCurrent(ctx, tx, orgID, sessionID, workerID, epoch)
		if err != nil {
			return err
		}
		if !current {
			return ErrStaleWorker
		}
		_, err = tx.Exec(ctx,
			`UPDATE ao_sandboxes
			SET startup_error_code = '',
				startup_error_message = '',
				startup_error_detail = '',
				startup_error_at = NULL,
				updated_at = now()
			WHERE session_id = $1 AND org_id = $2
				AND startup_error_code = ANY($3)`,
			sessionID, orgID, domain.WorkspaceStartupErrorCodes(),
		)
		if err != nil {
			return fmt.Errorf("clear workspace startup error: %w", err)
		}
		return nil
	})
}

// workspaceStartupErrorCodesSQL lists domain.WorkspaceStartupErrorCodes as
// SQL literals (a constant, so the constant session select can use it).
const workspaceStartupErrorCodesSQL = "'" + domain.WorkspaceStartupErrorCheckout + "', '" +
	domain.WorkspaceStartupErrorWorkingDirectory + "', '" + domain.WorkspaceStartupErrorProjectSetup + "', '" +
	domain.WorkspaceStartupErrorWorkingDirectoryOccupied + "', '" + domain.WorkspaceStartupErrorAgentStart + "'"

// workerBusyRecentSQL is how recently the worker's busy mark (last_busy_at,
// written by MarkSandboxBusy on a busy heartbeat, every 20 seconds while a
// Setup step runs) must have landed for a session with a failed workspace
// startup to count as retrying it. Three heartbeats, so one missed mark does
// not let a running retry be paused.
const workerBusyRecentSQL = "interval '1 minute'"

// queuedBehindFailedStartupSQL is the condition that turn (an ao_turns
// alias) is a queued turn of a session whose current workspace startup
// failed: its worker could not check the repository out or run the project's
// Setup and reported it on sandbox (an ao_sandboxes alias, see
// RecordWorkspaceStartupError). Its Chat controller claims no turn until the
// person reopens the session and the retry succeeds, so such a turn (a new
// session's task) is not work in flight: it does not keep the VM running,
// hold off idle pause, count as busy for the plan, or make the session read
// active. Opening the session still wakes it and drives the retry.
//
// The startup error stays set until the retry succeeds, and the control plane
// is not told a retry started (the desktop asks the worker directly), so
// while the worker's busy mark is recent (workerBusyRecentSQL: a retried
// Setup is running) the turn counts again: the retry is not paused, stopped
// or reclaimed for the plan mid-run.
func queuedBehindFailedStartupSQL(turn, sandbox string) string {
	return "(" + turn + ".state = 'queued' AND COALESCE(" + sandbox + ".startup_error_code, '') IN (" +
		workspaceStartupErrorCodesSQL + ") AND NOT COALESCE(" + sandbox + ".last_busy_at > now() - " +
		workerBusyRecentSQL + ", false))"
}

// truncateTail bounds text to limit bytes on a valid UTF-8 boundary, keeping
// the tail (the most recent output) when keepTail is set and the head
// otherwise.
func truncateTail(text string, limit int, keepTail bool) string {
	if len(text) <= limit {
		return text
	}
	if keepTail {
		return strings.ToValidUTF8(text[len(text)-limit:], "")
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
