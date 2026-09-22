package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/terminalview"
	"github.com/jackc/pgx/v5"
)

// UpsertTerminalViewer refreshes a socket's lease and reconciles the single
// worker PTY size. The session row is locked before the terminal row to match
// createWorkerRequest's lock order across control-plane replicas.
func (s *Store) UpsertTerminalViewer(
	ctx context.Context, terminal domain.TerminalSession, viewerID string,
	viewer terminalview.Viewer, lease time.Duration,
) (terminalview.Grid, error) {
	if (viewer.Role != "primary" && viewer.Role != "secondary") || lease <= 0 {
		return terminalview.Grid{}, ErrInvalid
	}
	return s.reconcileTerminalViewers(ctx, terminal, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO ao_terminal_viewers
				(id, org_id, session_id, terminal_id, role, visible, columns, rows, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::interval)
			 ON CONFLICT (id) DO UPDATE SET
				role = EXCLUDED.role, visible = EXCLUDED.visible,
				columns = EXCLUDED.columns, rows = EXCLUDED.rows,
				expires_at = EXCLUDED.expires_at
			 WHERE ao_terminal_viewers.org_id = EXCLUDED.org_id
			   AND ao_terminal_viewers.session_id = EXCLUDED.session_id
			   AND ao_terminal_viewers.terminal_id = EXCLUDED.terminal_id`,
			viewerID, terminal.OrgID, terminal.SessionID, terminal.ID,
			viewer.Role, viewer.Visible, viewer.Columns, viewer.Rows, intervalString(lease),
		)
		return err
	})
}

// RemoveTerminalViewer releases just this socket's lease, never the terminal.
func (s *Store) RemoveTerminalViewer(
	ctx context.Context, terminal domain.TerminalSession, viewerID string,
) (terminalview.Grid, error) {
	return s.reconcileTerminalViewers(ctx, terminal, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM ao_terminal_viewers
			 WHERE org_id = $1 AND session_id = $2 AND terminal_id = $3 AND id = $4`,
			terminal.OrgID, terminal.SessionID, terminal.ID, viewerID,
		)
		return err
	})
}

func (s *Store) TerminalGrid(
	ctx context.Context, terminal domain.TerminalSession,
) (terminalview.Grid, error) {
	var grid terminalview.Grid
	err := s.withOrg(ctx, terminal.OrgID, func(tx pgx.Tx) error {
		var columns, rowCount int
		if err := tx.QueryRow(ctx,
			`SELECT authoritative_columns, authoritative_rows
			 FROM ao_terminal_sessions
			 WHERE org_id = $1 AND session_id = $2 AND id = $3
			   AND worker_epoch = $4 AND state IN ('opening', 'open')
			   AND expires_at > now()`,
			terminal.OrgID, terminal.SessionID, terminal.ID, terminal.WorkerEpoch,
		).Scan(&columns, &rowCount); err != nil {
			return err
		}
		grid = terminalview.Grid{Columns: uint16(columns), Rows: uint16(rowCount)}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return terminalview.Grid{}, ErrStaleWorker
	}
	return grid, err
}

func (s *Store) reconcileTerminalViewers(
	ctx context.Context, terminal domain.TerminalSession, mutate func(pgx.Tx) error,
) (terminalview.Grid, error) {
	var result terminalview.Grid
	err := s.withOrg(ctx, terminal.OrgID, func(tx pgx.Tx) error {
		// Serialize with request creation and other terminal changes. The row is
		// also an epoch fence for a socket surviving a worker replacement.
		var epoch int64
		var sessionID string
		if err := tx.QueryRow(ctx,
			`SELECT id FROM ao_sessions WHERE org_id = $1 AND id = $2 FOR UPDATE`,
			terminal.OrgID, terminal.SessionID,
		).Scan(&sessionID); errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleWorker
		} else if err != nil {
			return err
		}
		var columns, rowCount int
		if err := tx.QueryRow(ctx,
			`SELECT worker_epoch, authoritative_columns, authoritative_rows
			 FROM ao_terminal_sessions
			 WHERE org_id = $1 AND session_id = $2 AND id = $3
			   AND state IN ('opening', 'open') AND expires_at > now()
			 FOR UPDATE`,
			terminal.OrgID, terminal.SessionID, terminal.ID,
		).Scan(&epoch, &columns, &rowCount); errors.Is(err, pgx.ErrNoRows) {
			return ErrStaleWorker
		} else if err != nil {
			return err
		}
		if epoch != terminal.WorkerEpoch {
			return ErrStaleWorker
		}
		result = terminalview.Grid{Columns: uint16(columns), Rows: uint16(rowCount)}
		if _, err := tx.Exec(ctx,
			`DELETE FROM ao_terminal_viewers
			 WHERE org_id = $1 AND session_id = $2 AND terminal_id = $3
			   AND expires_at <= now()`,
			terminal.OrgID, terminal.SessionID, terminal.ID,
		); err != nil {
			return err
		}
		if err := mutate(tx); err != nil {
			return err
		}

		rows, err := tx.Query(ctx,
			`SELECT role, visible, columns, rows FROM ao_terminal_viewers
			 WHERE org_id = $1 AND session_id = $2 AND terminal_id = $3
			   AND expires_at > now()`,
			terminal.OrgID, terminal.SessionID, terminal.ID,
		)
		if err != nil {
			return err
		}
		viewers := []terminalview.Viewer{}
		for rows.Next() {
			var viewer terminalview.Viewer
			var viewerColumns, viewerRows int
			if err := rows.Scan(&viewer.Role, &viewer.Visible, &viewerColumns, &viewerRows); err != nil {
				rows.Close()
				return err
			}
			viewer.Columns, viewer.Rows = uint16(viewerColumns), uint16(viewerRows)
			viewers = append(viewers, viewer)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}

		elected := terminalview.Elect(viewers)
		status := ""
		expired := false
		if (elected == result || elected == (terminalview.Grid{})) && result != (terminalview.Grid{}) {
			var expiresAt time.Time
			err := tx.QueryRow(ctx,
				`SELECT status, expires_at FROM ao_worker_requests
				 WHERE org_id = $1 AND session_id = $2 AND worker_epoch = $3
				   AND kind = 'terminal.resize' AND payload->>'terminalId' = $4
				   AND payload->>'columns' = $5 AND payload->>'rows' = $6
				 ORDER BY created_at DESC, id DESC LIMIT 1`,
				terminal.OrgID, terminal.SessionID, terminal.WorkerEpoch, terminal.ID,
				strconv.Itoa(int(result.Columns)), strconv.Itoa(int(result.Rows)),
			).Scan(&status, &expiresAt)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			expired = !expiresAt.IsZero() && !expiresAt.After(time.Now())
		}
		target, queue := selectTerminalResize(result, elected, status, expired)
		if !queue {
			return nil
		}
		payload, err := json.Marshal(map[string]any{
			"terminalId": terminal.ID, "columns": target.Columns, "rows": target.Rows,
		})
		if err != nil {
			return err
		}
		request, err := createWorkerRequest(
			ctx, tx, terminal.OrgID, terminal.SessionID, "terminal.resize", payload, 15*time.Second, "",
		)
		if err != nil {
			return err
		}
		if request.WorkerEpoch != terminal.WorkerEpoch {
			return ErrStaleWorker
		}
		if target != result {
			if _, err := tx.Exec(ctx,
				`UPDATE ao_terminal_sessions SET authoritative_columns = $1,
				 authoritative_rows = $2, authoritative_revision = authoritative_revision + 1,
				 updated_at = now()
				 WHERE org_id = $3 AND session_id = $4 AND id = $5 AND worker_epoch = $6`,
				target.Columns, target.Rows, terminal.OrgID, terminal.SessionID, terminal.ID, terminal.WorkerEpoch,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SELECT pg_notify('ao_terminal_size', $1)`, terminal.ID); err != nil {
				return err
			}
		}
		result = target
		return nil
	})
	return result, err
}

// selectTerminalResize keeps the last PTY grid when nobody is viewing, avoids
// duplicate worker requests, and retries a resize that never completed.
func selectTerminalResize(
	current, elected terminalview.Grid,
	requestStatus string,
	requestExpired bool,
) (terminalview.Grid, bool) {
	target := elected
	if target == (terminalview.Grid{}) {
		target = current
	}
	if target == (terminalview.Grid{}) {
		return target, false
	}
	if target != current {
		return target, true
	}
	return target, requestStatus == "" || requestStatus == "failed" || requestStatus == "cancelled" ||
		(requestExpired && requestStatus != "succeeded")
}
