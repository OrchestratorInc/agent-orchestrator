package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"maps"
	"strings"
)

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

// Completion belongs to the database it describes. Pure SQL triggers also work
// in older binaries: a legacy write invalidates completion before it commits.
// No full-table scan is needed to validate a clean database at startup.
func prepareTimestampProgress(ctx context.Context, db *sql.DB, columns []timestampColumn) error {
	// Standalone repair callers can operate on a database without goose history.
	migration, err := migrationsFS.ReadFile("migrations/20261116173733857_timestamp_repair_progress.sql")
	if err != nil {
		return err
	}
	up := strings.SplitN(string(migration), "-- +goose Down", 2)[0]
	up = strings.ReplaceAll(up, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ")
	up = strings.ReplaceAll(up, "INSERT INTO timestamp_repair_control", "INSERT OR IGNORE INTO timestamp_repair_control")
	if _, err := db.ExecContext(ctx, up); err != nil {
		return err
	}
	existing, err := timestampProgressTriggerDefinitions(ctx, db)
	if err != nil {
		return err
	}
	orphaned := maps.Clone(existing)
	for i := 0; i < len(columns); {
		end := i + 1
		for end < len(columns) && columns[end].table == columns[i].table {
			end++
		}
		group := columns[i:end]
		i = end
		table := group[0].table
		triggers := timestampProgressTriggers(group)
		changed := false
		for name, text := range triggers {
			delete(orphaned, name)
			if existing[name] != text {
				changed = true
			}
		}
		if !changed {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO timestamp_repair_progress(table_name) VALUES(?) ON CONFLICT(table_name) DO UPDATE SET completed=0,generation=generation+1`, table); err == nil {
			for name, text := range triggers {
				if existing[name] == text {
					continue
				}
				if _, err = tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+quoteIdentifier(name)); err != nil {
					break
				}
				if _, err = tx.ExecContext(ctx, text); err != nil {
					break
				}
			}
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return dropTimestampProgressTriggers(ctx, db, orphaned)
}

func timestampProgressTriggers(columns []timestampColumn) map[string]string {
	table := columns[0].table
	literal := "'" + strings.ReplaceAll(table, "'", "''") + "'"
	old, fresh, changed := make([]string, 0, len(columns)), make([]string, 0, len(columns)), make([]string, 0, len(columns))
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		name := quoteIdentifier(column.column)
		predicate := legacyTimestampPredicate(column.column)
		before := strings.ReplaceAll(predicate, name, "OLD."+name)
		after := strings.ReplaceAll(predicate, name, "NEW."+name)
		old = append(old, before)
		fresh = append(fresh, after)
		changed = append(changed, "(OLD."+name+" IS NOT NEW."+name+" AND (("+before+") OR ("+after+")))")
		names = append(names, name)
	}
	predicates := map[string]string{
		"insert": strings.Join(fresh, " OR "),
		"update": strings.Join(changed, " OR "),
		"delete": strings.Join(old, " OR "),
	}
	out := map[string]string{}
	for event, predicate := range predicates {
		name := fmt.Sprintf("ao_timestamp_%x_%s", sha256.Sum256([]byte(table)), event)
		action := strings.ToUpper(event)
		if event == "update" {
			action += " OF " + strings.Join(names, ",")
		}
		out[name] = "CREATE TRIGGER " + quoteIdentifier(name) + " AFTER " + action + " ON " + quoteIdentifier(table) +
			" WHEN (SELECT paused FROM timestamp_repair_control WHERE id=1)=0 AND (" + predicate + ") BEGIN " +
			"UPDATE timestamp_repair_progress SET generation=generation+1,completed=0 WHERE table_name=" + literal + "; END"
	}
	return out
}

func beginTimestampProgress(ctx context.Context, db *sql.DB, table, signature string) (generation int64, completed bool, err error) {
	_, err = db.ExecContext(ctx, `INSERT INTO timestamp_repair_progress(table_name,signature) VALUES(?,?) ON CONFLICT(table_name) DO UPDATE SET signature=excluded.signature,completed=0,generation=generation+1 WHERE signature<>excluded.signature`, table, signature)
	if err != nil {
		return 0, false, err
	}
	err = db.QueryRowContext(ctx, `SELECT generation,completed FROM timestamp_repair_progress WHERE table_name=?`, table).Scan(&generation, &completed)
	return generation, completed, err
}

func finishTimestampProgress(ctx context.Context, db *sql.DB, table, signature string, generation int64, rejected bool) error {
	result, err := db.ExecContext(ctx, `UPDATE timestamp_repair_progress SET completed=1,rejected=? WHERE table_name=? AND signature=? AND generation=?`, rejected, table, signature, generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("timestamp repair of %s invalidated by a concurrent write; retry required", table)
	}
	return nil
}
