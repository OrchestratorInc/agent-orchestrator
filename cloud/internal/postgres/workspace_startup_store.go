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
// worker reports the workspace ready.
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
				startup_error_at = CASE
					WHEN startup_error_code IS DISTINCT FROM $3
						OR startup_error_message IS DISTINCT FROM $4
						OR startup_error_at IS NULL
						THEN now()
					ELSE startup_error_at
				END,
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
