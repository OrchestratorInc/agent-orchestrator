package timestamps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RepresentationDB is the caller's transaction or reserved connection.
type RepresentationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// PreserveRepresentation runs inside the caller's write transaction. Its
// callback counts direct writes and compensated revision writes. Any other
// trigger effect makes the caller roll back instead of publishing changed facts.
func PreserveRepresentation(ctx context.Context, db RepresentationDB, rewrite func() (int64, error)) (err error) {
	var progress bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='timestamp_repair_control')`).Scan(&progress); err != nil {
		return err
	}
	if progress {
		var paused int
		if err := db.QueryRowContext(ctx, `SELECT paused FROM timestamp_repair_control WHERE id=1`).Scan(&paused); err != nil {
			return err
		}
		if paused == 0 {
			// The caller owns the write transaction. Other writers cannot see this
			// pause, and rollback restores it if normalization or cleanup fails.
			if _, err := db.ExecContext(ctx, `UPDATE timestamp_repair_control SET paused=1 WHERE id=1`); err != nil {
				return err
			}
			defer func() {
				_, restoreErr := db.ExecContext(ctx, `UPDATE timestamp_repair_control SET paused=0 WHERE id=1`)
				err = errors.Join(err, restoreErr)
			}()
		}
	}
	var start, water int64
	var sequence sql.NullInt64
	var logs bool
	if err := db.QueryRowContext(ctx, `SELECT total_changes(),EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='change_log')`).Scan(&start, &logs); err != nil {
		return err
	}
	if logs {
		if err := db.QueryRowContext(ctx, `SELECT (SELECT seq FROM sqlite_sequence WHERE name='change_log'),coalesce((SELECT max(seq) FROM change_log),0)`).Scan(&sequence, &water); err != nil {
			return err
		}
	}
	expected, err := rewrite()
	if err != nil {
		return err
	}
	if logs {
		result, err := db.ExecContext(ctx, `DELETE FROM change_log WHERE seq>?`, water)
		if err != nil {
			return err
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		expected += 2 * removed
		if sequence.Valid {
			result, err = db.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=? WHERE name='change_log' AND seq<>?`, sequence.Int64, sequence.Int64)
		} else {
			result, err = db.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name='change_log'`)
		}
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		expected += changed
	}
	var end int64
	if err := db.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&end); err != nil {
		return err
	}
	if end-start != expected {
		return fmt.Errorf("unexpected timestamp repair writes: observed %d, expected %d", end-start, expected)
	}
	return nil
}
