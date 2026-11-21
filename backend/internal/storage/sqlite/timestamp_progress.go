package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"maps"
	"strings"
)

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
	old, fresh := make([]string, 0, len(columns)), make([]string, 0, len(columns))
	for _, column := range columns {
		predicate := legacyTimestampPredicate(column.column)
		old = append(old, strings.ReplaceAll(predicate, quoteIdentifier(column.column), "OLD."+quoteIdentifier(column.column)))
		fresh = append(fresh, strings.ReplaceAll(predicate, quoteIdentifier(column.column), "NEW."+quoteIdentifier(column.column)))
	}
	rejected := `(SELECT rejected FROM timestamp_repair_progress WHERE table_name=` + literal + `)=1`
	predicates := map[string]string{
		"insert": strings.Join(fresh, " OR "),
		"update": strings.Join(append(append([]string{}, old...), fresh...), " OR "),
		"delete": strings.Join(old, " OR "),
	}
	out := map[string]string{}
	for event, predicate := range predicates {
		name := fmt.Sprintf("ao_timestamp_%x_%s", sha256.Sum256([]byte(table)), event)
		out[name] = "CREATE TRIGGER " + quoteIdentifier(name) + " AFTER " + strings.ToUpper(event) + " ON " + quoteIdentifier(table) +
			" WHEN (SELECT paused FROM timestamp_repair_control WHERE id=1)=0 AND (" + rejected + " OR " + predicate + ") BEGIN " +
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
