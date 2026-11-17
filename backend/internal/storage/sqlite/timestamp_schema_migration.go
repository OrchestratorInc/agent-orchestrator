package sqlite

import (
	"context"
	"database/sql"
	"strings"
)

// Ordinary opens retain their trigger DDL. Pending migrations use guards that
// invalidate only written tables and do not reference removable columns.
func prepareTimestampSchemaMigration(ctx context.Context, db *sql.DB, versions []int64) (map[string]string, error) {
	triggers, err := timestampProgressTriggerDefinitions(ctx, db)
	if err != nil || len(triggers) == 0 {
		return nil, err
	}
	var hasHistory bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version')`).Scan(&hasHistory); err != nil {
		return nil, err
	}
	applied := map[int64]bool{}
	if hasHistory {
		rows, err := db.QueryContext(ctx, `SELECT version_id,is_applied FROM goose_db_version ORDER BY id DESC`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var version int64
			var done bool
			if err := rows.Scan(&version, &done); err != nil {
				return nil, err
			}
			if _, exists := applied[version]; !exists {
				applied[version] = done
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
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
		return nil, nil
	}
	if err := suspendTimestampProgressTriggers(ctx, db, triggers); err != nil {
		return nil, err
	}
	return triggers, nil
}

func timestampProgressTriggerDefinitions(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE type='trigger' AND name GLOB 'ao_timestamp_*'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	existing := map[string]string{}
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			return nil, err
		}
		existing[name] = text
	}
	return existing, rows.Err()
}

func dropTimestampProgressTriggers(ctx context.Context, db *sql.DB, triggers map[string]string) error {
	if len(triggers) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for name := range triggers {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdentifier(name)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Migration guards track writes without naming columns that DDL may remove.
// A rebuilt table loses its guards, which also forces a repair pass.
func suspendTimestampProgressTriggers(ctx context.Context, db *sql.DB, previous map[string]string) error {
	if len(previous) == 0 {
		return nil
	}
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for name := range previous {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdentifier(name)); err != nil {
			return err
		}
	}
	for i := 0; i < len(columns); {
		end := i + 1
		for end < len(columns) && columns[end].table == columns[i].table {
			end++
		}
		table := columns[i].table
		for name := range timestampProgressTriggers(columns[i:end]) {
			if previous[name] == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, timestampMigrationTrigger(name, table)); err != nil {
				return err
			}
		}
		i = end
	}
	return tx.Commit()
}

func timestampMigrationTrigger(name, table string) string {
	event := strings.ToUpper(name[strings.LastIndexByte(name, '_')+1:])
	return "CREATE TRIGGER " + quoteIdentifier(name) + " AFTER " + event + " ON " + quoteIdentifier(table) +
		" BEGIN UPDATE timestamp_repair_progress SET generation=generation+1,completed=0 WHERE table_name='" + strings.ReplaceAll(table, "'", "''") + "'; END"
}

// Restore only definitions that still match the live schema. Missing or changed
// triggers remain absent so preparation invalidates their tables before repair.
func restoreTimestampProgressTriggers(ctx context.Context, db *sql.DB, previous map[string]string) error {
	if len(previous) == 0 {
		return nil
	}
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		return err
	}
	suspended, err := timestampProgressTriggerDefinitions(ctx, db)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for name := range previous {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdentifier(name)); err != nil {
			return err
		}
	}
	for i := 0; i < len(columns); {
		end := i + 1
		for end < len(columns) && columns[end].table == columns[i].table {
			end++
		}
		for name, text := range timestampProgressTriggers(columns[i:end]) {
			if previous[name] == text && suspended[name] == timestampMigrationTrigger(name, columns[i].table) {
				if _, err := tx.ExecContext(ctx, text); err != nil {
					return err
				}
			}
		}
		i = end
	}
	return tx.Commit()
}
