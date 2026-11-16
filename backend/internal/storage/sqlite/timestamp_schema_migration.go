package sqlite

import (
	"context"
	"database/sql"
)

// Tracking triggers refer to the current timestamp columns. Remove them before
// pending migrations can drop those columns, and invalidate completion before
// any migration writes can go untracked. Ordinary opens leave them intact.
func prepareTimestampSchemaMigration(ctx context.Context, db *sql.DB, versions []int64) error {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='trigger' AND name GLOB 'ao_timestamp_*'`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		triggers = append(triggers, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(triggers) == 0 {
		return nil
	}
	var hasHistory bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version')`).Scan(&hasHistory); err != nil {
		return err
	}
	applied := map[int64]bool{}
	if hasHistory {
		rows, err := db.QueryContext(ctx, `SELECT version_id,is_applied FROM goose_db_version ORDER BY id DESC`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var version int64
			var done bool
			if err := rows.Scan(&version, &done); err != nil {
				return err
			}
			if _, exists := applied[version]; !exists {
				applied[version] = done
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	pending := false
	for _, version := range versions {
		if !applied[version] {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE timestamp_repair_progress SET completed=0`); err != nil {
		return err
	}
	for _, name := range triggers {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteIdentifier(name)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
