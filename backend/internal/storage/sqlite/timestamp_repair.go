package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	modernsqlite "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// Kept for older builds; a completed pass is no proof an old writer has not
// subsequently stored more local times. Never use this marker to skip detection.
const offsetOnlyTimestampRepairVersion = 1
const timestampRepairBatch = 256

// These tables feed timestamp predicates, keyset pages, or newest-row decisions.
// Repair them before the store, search readers, and schedulers become available.
var timestampFenceTables = map[string]bool{
	"reports": true, "notifications": true, "automations": true, "automation_runs": true,
	"sessions": true, "conversation_turns": true, "session_agent_accounts": true,
	"session_cleanup_facts": true, "usage_sources": true, "usage_bindings": true,
	"model_usage_events": true, "change_log": true, "telemetry_event": true,
	"conversation_messages": true, "conversation_activities": true, "conversations": true,
	"conversation_branches": true, "session_interface_transitions": true,
	"pr": true, "pr_checks": true, "pr_comment": true, "pr_reviews": true,
	"pr_review_threads": true, "review": true, "review_run": true,
	"agent_accounts": true, "shell_terminals": true, "session_artifacts": true,
	"agent_switches": true, "session_forks": true, "codex_account_switches": true,
}

// Scan forward by row identity, with bounded memory and write transactions.
// Related timestamps change together so row CHECK constraints remain valid.
// SQL-side UTC defaults remain unchanged. Invalid dates are preserved.
func repairTimestamps(ctx context.Context, db *sql.DB) error {
	return repairTimestampTables(ctx, db, false)
}

func repairTimestampTables(ctx context.Context, db *sql.DB, fence bool) error {
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		return err
	}
	var failures []error
	for i := 0; i < len(columns); {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		end := i + 1
		for end < len(columns) && columns[end].table == columns[i].table {
			end++
		}
		group := columns[i:end]
		i = end
		if fence && !timestampFenceTables[group[0].table] {
			continue
		}
		cursor, lastRow := int64(math.MinInt64), int64(math.MaxInt64)
		if fence {
			var done bool
			cursor, lastRow, done, err = timestampRepairBounds(ctx, db, group)
			if err != nil {
				failures = append(failures, fmt.Errorf("detect %s: %w", group[0].table, err))
				continue
			}
			if done {
				continue
			}
		}
		for {
			next, done, err := repairTimestampBatchMode(ctx, db, group, cursor, fence, lastRow)
			if err != nil {
				failures = append(failures, fmt.Errorf("repair %s: %w", group[0].table, err))
				break
			}
			if done {
				break
			}
			cursor = next
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < offsetOnlyTimestampRepairVersion {
		_, err = db.ExecContext(ctx, `PRAGMA user_version=1`)
	}
	return err
}

func repairTimestampBatch(ctx context.Context, db *sql.DB, columns []timestampColumn, cursor int64) (int64, bool, error) {
	return repairTimestampBatchMode(ctx, db, columns, cursor, false, math.MaxInt64)
}

func repairTimestampBatchMode(ctx context.Context, db *sql.DB, columns []timestampColumn, cursor int64, candidatesOnly bool, lastRow int64) (int64, bool, error) {
	table := quoteIdentifier(columns[0].table)
	fields, sets, checks := make([]string, len(columns)), make([]string, len(columns)), make([]string, len(columns))
	for i, c := range columns {
		column := quoteIdentifier(c.column)
		fields[i] = fmt.Sprintf("CASE WHEN %s THEN CAST(%s AS TEXT) END", legacyTimestampPredicate(c.column), column)
		sets[i] = fmt.Sprintf("%s=coalesce(?,%s)", column, column)
		checks[i] = fmt.Sprintf("(? IS NULL OR (typeof(%[1]s)='text' AND CAST(%[1]s AS TEXT)=?))", column)
	}
	// Clean rows also consume the scan budget, so detection cannot hold the
	// sole writer connection for an entire clean or sparsely affected table.
	filter := ""
	if candidatesOnly {
		// Startup has no readers or competing writers yet. Filter inside SQLite
		// so clean rows never cross the driver boundary or allocate Go values.
		predicates := make([]string, len(fields))
		for i, field := range fields {
			predicates[i] = "(" + field + ") IS NOT NULL"
		}
		filter = " AND (" + strings.Join(predicates, " OR ") + ")"
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT rowid,%s FROM %s WHERE rowid>=? AND rowid<=?%s ORDER BY rowid LIMIT ?`, strings.Join(fields, ","), table, filter), cursor, lastRow, timestampRepairBatch)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	var changes [][]any
	var id int64
	values, dest := make([]sql.NullString, len(columns)), make([]any, len(columns)+1)
	dest[0] = &id
	for i := range values {
		dest[i+1] = &values[i]
	}
	n, last := 0, cursor
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return 0, false, err
		}
		n++
		last = id
		var args, before []any
		for i, value := range values {
			if !value.Valid {
				continue
			}
			if at, ok := timestamps.ParseLegacy(value.String); ok && at.String() != value.String {
				if args == nil {
					args, before = make([]any, len(columns)+1, 3*len(columns)+1), make([]any, len(columns))
				}
				args[i], before[i] = at, value.String
			}
		}
		if args != nil {
			args[len(columns)] = id
			for _, value := range before {
				args = append(args, value, value)
			}
			changes = append(changes, args)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if len(changes) > 0 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = tx.Rollback() }()
		var hasChangeLog bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='change_log')`).Scan(&hasChangeLog); err != nil {
			return 0, false, err
		}
		var sequence sql.NullInt64
		var logHighWater int64
		if hasChangeLog {
			if err := tx.QueryRowContext(ctx, `SELECT (SELECT seq FROM sqlite_sequence WHERE name='change_log'), COALESCE((SELECT max(seq) FROM change_log),0)`).Scan(&sequence, &logHighWater); err != nil {
				return 0, false, err
			}
		}
		query := fmt.Sprintf(`UPDATE %s SET %s WHERE rowid=? AND %s`, table, strings.Join(sets, ","), strings.Join(checks, " AND "))
		if columns[0].table == "sessions" {
			// RETURNING sees the revision before the AFTER UPDATE trigger runs.
			query += ` RETURNING revision`
		}
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = stmt.Close() }()
		for _, v := range changes {
			// A concurrent writer may have replaced this value since detection.
			if columns[0].table == "sessions" {
				var revision int64
				if err := stmt.QueryRowContext(ctx, v...).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
					continue
				} else if err != nil {
					return 0, false, err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revision=? WHERE rowid=? AND revision<>?`, revision, v[len(columns)], revision); err != nil {
					return 0, false, err
				}
			} else if _, err = stmt.ExecContext(ctx, v...); err != nil {
				var constraint *modernsqlite.Error
				if !errors.As(err, &constraint) || (constraint.Code() != 2067 && constraint.Code() != 1555) {
					return 0, false, err
				}
				if err := resolveTimestampCollision(ctx, tx, columns, v); err != nil {
					return 0, false, err
				}
				if _, err := stmt.ExecContext(ctx, v...); err != nil {
					return 0, false, err
				}
			}
		}
		// These are representation changes, so publish no logical write events.
		// The writer transaction hides its generated events until they are removed;
		// pre-existing events and all validation triggers remain intact.
		if hasChangeLog {
			if _, err := tx.ExecContext(ctx, `DELETE FROM change_log WHERE seq>?`, logHighWater); err != nil {
				return 0, false, err
			}
			if sequence.Valid {
				_, err = tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=? WHERE name='change_log' AND seq<>?`, sequence.Int64, sequence.Int64)
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name='change_log'`)
			}
			if err != nil {
				return 0, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
	}
	return last + 1, n < timestampRepairBatch || last == math.MaxInt64, nil
}

func legacyTimestampPredicate(column string) string {
	return fmt.Sprintf(`typeof(%[1]s)='text'
AND ((%[1]s GLOB '* [+-][0-9][0-9][0-9][0-9] *' AND %[1]s NOT GLOB '* +0000 UTC')
OR %[1]s GLOB '????-??-??T*')`, quoteIdentifier(column))
}

// A covering index avoids loading transcript/command payloads just to discover
// that their timestamps are already UTC. Bounds keep repair linear even when
// an entire old database needs rewriting; only two row IDs stay in memory.
func timestampRepairBounds(ctx context.Context, db *sql.DB, columns []timestampColumn) (int64, int64, bool, error) {
	args := []any{columns[0].table}
	predicates := make([]string, len(columns))
	for i, column := range columns {
		args = append(args, column.column)
		predicates[i] = "(" + legacyTimestampPredicate(column.column) + ")"
	}
	args = append(args, len(columns))
	var index string
	err := db.QueryRowContext(ctx, `SELECT il.name FROM pragma_index_list(?) il
 WHERE il.partial=0 AND (SELECT count(DISTINCT name) FROM pragma_index_info(il.name)
 WHERE name IN (`+strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")+`))=?
 ORDER BY (SELECT count(*) FROM pragma_index_info(il.name)) LIMIT 1`, args...).Scan(&index)
	if errors.Is(err, sql.ErrNoRows) {
		return math.MinInt64, math.MaxInt64, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	var first, last sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT min(rowid),max(rowid) FROM `+quoteIdentifier(columns[0].table)+` INDEXED BY `+quoteIdentifier(index)+` WHERE `+strings.Join(predicates, " OR ")).Scan(&first, &last)
	return first.Int64, last.Int64, !first.Valid, err
}

// Resolve only collisions whose logical winner is known. Never replace a run
// carrying execution history with a freshly materialized pending occurrence.
func resolveTimestampCollision(ctx context.Context, tx *sql.Tx, columns []timestampColumn, values []any) error {
	table := columns[0].table
	id := values[len(columns)]
	key := "bound_at"
	if table == "automation_runs" {
		key = "scheduled_for"
	}
	var at any
	for i, column := range columns {
		if column.column == key {
			at = values[i]
		}
	}
	var twin int64
	switch table {
	case "session_agent_accounts":
		if err := tx.QueryRowContext(ctx, `SELECT twin.rowid FROM session_agent_accounts original JOIN session_agent_accounts twin
 ON twin.session_id=original.session_id AND twin.provider=original.provider AND twin.bound_at=?
 WHERE original.rowid=? AND twin.rowid<>original.rowid`, at, id).Scan(&twin); err != nil {
			return err
		}
		// BindSessionAgentAccount overwrites an existing same-instant binding.
		// Rowid records which of the differently spelled bindings was written last.
		if id.(int64) > twin {
			id = twin
		}
	case "automation_runs":
		if err := tx.QueryRowContext(ctx, `SELECT twin.rowid FROM automation_runs original JOIN automation_runs twin
 ON twin.automation_id=original.automation_id AND twin.scheduled_for=?
 WHERE original.rowid=? AND twin.rowid<>original.rowid`, at, id).Scan(&twin); err != nil {
			return err
		}
		empty := func(row any) (bool, error) {
			var unused bool
			err := tx.QueryRowContext(ctx, `SELECT status='pending' AND attempt_count=0 AND session_id IS NULL
 AND claimed_at IS NULL AND lease_expires_at IS NULL AND started_at IS NULL AND finished_at IS NULL
 AND coalesce(error_message,'')='' AND NOT EXISTS(SELECT 1 FROM sessions WHERE automation_run_id=automation_runs.id)
 FROM automation_runs WHERE rowid=?`, row).Scan(&unused)
			return unused, err
		}
		originalEmpty, err := empty(id)
		if err != nil {
			return err
		}
		twinEmpty, err := empty(twin)
		if err != nil {
			return err
		}
		if !originalEmpty && !twinEmpty {
			return fmt.Errorf("automation occurrence collision at rowids %v and %d has two execution histories", id, twin)
		}
		if !originalEmpty {
			id = twin
		}
	default:
		return fmt.Errorf("unhandled timestamp key collision in %s", table)
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM "+quoteIdentifier(table)+" WHERE rowid=?", id)
	return err
}

type timestampColumn struct{ table, column string }

func timestampColumns(ctx context.Context, db *sql.DB) ([]timestampColumn, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.name,p.name FROM sqlite_master AS m,pragma_table_info(m.name) AS p
WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%' AND m.sql NOT LIKE 'CREATE VIRTUAL%'
AND upper(p.type) IN ('TIMESTAMP','DATETIME','DATE') ORDER BY m.name,p.cid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var columns []timestampColumn
	for rows.Next() {
		var c timestampColumn
		if err := rows.Scan(&c.table, &c.column); err != nil {
			return nil, err
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}
func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
