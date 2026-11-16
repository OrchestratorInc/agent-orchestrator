package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	modernsqlite "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// Kept for older builds; a completed pass is no proof an old writer has not
// subsequently stored more local times. Never use this marker to skip detection.
const offsetOnlyTimestampRepairVersion = 1
const timestampRepairBatch = 256

var errTimestampPolicySkip = errors.New("preserved distinct execution history")

// These tables feed timestamp predicates, keyset pages, or newest-row decisions.
// Repair them before the store, search readers, and schedulers become available.
var timestampFenceTables = map[string]bool{
	"reports": true, "notifications": true, "automations": true, "automation_runs": true,
	"sessions": true, "conversation_turns": true, "session_agent_accounts": true,
	"session_cleanup_facts": true, "usage_sources": true, "usage_bindings": true,
	"model_usage_events": true, "session_interface_transitions": true,
	"codex_account_switches": true,

	"review": true, "review_run": true,
	"conversations": true, "conversation_branches": true, "conversation_messages": true, "conversation_activities": true,
	"pr": true, "pr_checks": true, "pr_reviews": true, "pr_comment": true, "pr_review_threads": true,
	"change_log": true, "agent_accounts": true, "session_artifacts": true,
	"session_forks": true, "telemetry_event": true, "shell_terminals": true, "kept_artifact_sessions": true,
}

// Scan forward by row identity, with bounded memory and write transactions.
// Related timestamps change together so row CHECK constraints remain valid.
// SQL-side UTC defaults remain unchanged. Invalid dates are preserved.
func repairTimestamps(ctx context.Context, db *sql.DB) error {
	return repairTimestampTables(ctx, db, false)
}

func repairTimestampTables(ctx context.Context, db *sql.DB, fence bool) error {
	result, err := runTimestampRepair(ctx, db, fence)
	slog.Info("timestamp repair pass", "repaired", result.Repaired, "deleted", result.Deleted, "skipped", result.Skipped, "foreground", fence)
	return err
}

func runTimestampRepair(ctx context.Context, db *sql.DB, fence bool) (result timestampRepairResult, err error) {
	return runTimestampRepairState(ctx, db, db, fence, newTimestampRepairState())
}

func runTimestampRepairState(ctx context.Context, db, reader *sql.DB, fence bool, state *timestampRepairState) (result timestampRepairResult, err error) {
	state.reader = reader
	state.timestampRepairResult = timestampRepairResult{}
	state.retry = map[string]bool{}
	defer func() { result = state.timestampRepairResult }()
	columns, err := timestampColumns(ctx, reader)
	if err != nil {
		return result, err
	}
	if err := prepareTimestampProgress(ctx, db, columns); err != nil {
		return result, err
	}
	var failures []error
	for i := 0; i < len(columns); {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
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
		table := group[0].table
		names := make([]string, len(group))
		for j, column := range group {
			names[j] = column.column
		}
		var schema string
		if err := reader.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&schema); err != nil {
			return result, err
		}
		signature := "v1\x00" + strings.Join(names, "\x00") + "\x00" + schema
		generation, completed, err := beginTimestampProgress(ctx, db, table, signature)
		if err != nil {
			return result, err
		}
		if completed {
			continue
		}
		skippedBefore := state.Skipped
		complete := func() {
			if err := finishTimestampProgress(ctx, db, table, signature, generation, state.Skipped > skippedBefore); err != nil {
				failures = append(failures, err)
			}
		}
		cursor, lastRow := int64(math.MinInt64), int64(math.MaxInt64)
		if fence {
			var done bool
			cursor, lastRow, done, err = timestampRepairBounds(ctx, reader, group)
			if err != nil {
				failures = append(failures, fmt.Errorf("detect %s: %w", group[0].table, err))
				continue
			}
			if done {
				complete()
				continue
			}
		}
		for {
			next, done, err := repairTimestampBatchMode(ctx, db, group, cursor, fence, lastRow, state)
			if err != nil {
				failures = append(failures, fmt.Errorf("repair %s: %w", group[0].table, err))
				break
			}
			if done {
				if !state.retry[table] {
					complete()
				} else if fence {
					failures = append(failures, fmt.Errorf("timestamp ordering repair remains pending for %s", table))
				}
				break
			}
			cursor = next
		}
	}
	if len(failures) > 0 {
		return result, errors.Join(failures...)
	}
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return result, err
	}
	if version < offsetOnlyTimestampRepairVersion {
		_, err = db.ExecContext(ctx, `PRAGMA user_version=1`)
	}
	return result, err
}

func repairTimestampBatch(ctx context.Context, db *sql.DB, columns []timestampColumn, cursor int64) (int64, bool, error) {
	return repairTimestampBatchMode(ctx, db, columns, cursor, false, math.MaxInt64, nil)
}

func repairTimestampBatchMode(ctx context.Context, db *sql.DB, columns []timestampColumn, cursor int64, candidatesOnly bool, lastRow int64, state *timestampRepairState) (next int64, done bool, err error) {
	if state == nil {
		state = newTimestampRepairState()
	}
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
	queryRows := strings.Join([]string{"SELECT rowid,", strings.Join(fields, ","), " FROM ", table, " WHERE rowid>=? AND rowid<=?", filter, " ORDER BY rowid LIMIT ?"}, "")
	reader := state.reader
	if reader == nil {
		reader = db
	}
	rows, err := reader.QueryContext(ctx, queryRows, cursor, lastRow, timestampRepairBatch)
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
		tx, err := db.Conn(ctx)
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = tx.Close() }()
		if _, err := tx.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return 0, false, err
		}
		defer func() { _, _ = tx.ExecContext(context.Background(), "ROLLBACK") }()
		query := strings.Join([]string{"UPDATE", table, "SET", strings.Join(sets, ","), "WHERE rowid=? AND", strings.Join(checks, " AND ")}, " ")
		if columns[0].table == "sessions" {
			query += " RETURNING revision"
		}
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = stmt.Close() }()
		var repaired, deleted int
		var bindingDrops []*timestampBindingDrop
		err = timestamps.PreserveRepresentation(ctx, tx, func() (int64, error) {
			// Most batches have no constraints. Save one batch instead of creating
			// and measuring a savepoint for every successfully normalized row.
			if _, err := tx.ExecContext(ctx, "SAVEPOINT timestamp_batch"); err != nil {
				return 0, err
			}
			var before int64
			if err := tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&before); err != nil {
				return 0, err
			}
			var expected int64
			var batchErr error
			missed := false
			for _, values := range changes {
				writes, updated, err := applyTimestampUpdate(ctx, tx, stmt, columns, values)
				if err != nil {
					batchErr = err
					break
				}
				expected += writes
				if updated {
					repaired++
				} else {
					missed = true
				}
			}
			if batchErr == nil {
				if missed {
					state.retry[columns[0].table] = true
				}
				_, err := tx.ExecContext(ctx, "RELEASE timestamp_batch")
				return expected, err
			}
			var constraint *modernsqlite.Error
			if !errors.As(batchErr, &constraint) || constraint.Code()&255 != 19 {
				return 0, batchErr
			}
			if _, err := tx.ExecContext(ctx, "ROLLBACK TO timestamp_batch"); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, "RELEASE timestamp_batch"); err != nil {
				return 0, err
			}
			// SQLite total_changes includes rolled-back writes and trigger effects.
			// Count them separately; their change-log rows no longer exist.
			var after int64
			if err := tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&after); err != nil {
				return 0, err
			}
			expected, repaired = after-before, 0
			for _, values := range changes {
				if _, err := tx.ExecContext(ctx, "SAVEPOINT timestamp_row"); err != nil {
					return 0, err
				}
				if err := tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&before); err != nil {
					return 0, err
				}
				writes, updated, rowErr := applyTimestampUpdate(ctx, tx, stmt, columns, values)
				var bindingDrop *timestampBindingDrop
				removed := false
				if rowErr != nil && errors.As(rowErr, &constraint) && (constraint.Code() == 2067 || constraint.Code() == 1555) {
					var resolved error
					bindingDrop, resolved = resolveTimestampCollision(ctx, tx, columns, values)
					if resolved != nil {
						rowErr = errors.Join(rowErr, resolved)
					} else {
						removed = true
						writes, updated, rowErr = applyTimestampUpdate(ctx, tx, stmt, columns, values)
						writes++
					}
				}
				if rowErr != nil {
					if !preserveRejectedTimestamp(rowErr) {
						return 0, rowErr
					}
					if _, err := tx.ExecContext(ctx, "ROLLBACK TO timestamp_row"); err != nil {
						return 0, err
					}
					if err := tx.QueryRowContext(ctx, "SELECT total_changes()").Scan(&after); err != nil {
						return 0, err
					}
					expected += after - before
					state.skip(columns[0].table, values, len(columns), rowErr)
				} else {
					expected += writes
					if updated {
						repaired++
					} else if !removed {
						state.retry[columns[0].table] = true
					}
					if removed {
						deleted++
						if bindingDrop != nil {
							bindingDrops = append(bindingDrops, bindingDrop)
						}
					}
				}
				if _, err := tx.ExecContext(ctx, "RELEASE timestamp_row"); err != nil {
					return 0, err
				}
			}
			return expected, nil
		})
		if err != nil {
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx, "COMMIT"); err != nil {
			return 0, false, err
		}
		state.Repaired += repaired
		state.Deleted += deleted
		for _, drop := range bindingDrops {
			slog.Warn("timestamp repair dropped same-instant account binding", "session", drop.session, "provider", drop.provider,
				"kept_account", drop.keptAccount, "dropped_account", drop.droppedAccount, "dropped_rowid", drop.rowid)
		}

	}
	return last + 1, n < timestampRepairBatch || last == math.MaxInt64, nil
}

// A representation change must not discard execution history or rewrite a row
// into a CHECK violation. Other failures still abort repair and startup.
func preserveRejectedTimestamp(err error) bool {
	if errors.Is(err, errTimestampPolicySkip) {
		return true
	}
	var constraint *modernsqlite.Error
	return errors.As(err, &constraint) && constraint.Code() == 275 // SQLITE_CONSTRAINT_CHECK
}

// applyTimestampUpdate counts only direct writes and the session revision
// compensation. Collision handling is reserved for the constraint fallback.
func applyTimestampUpdate(ctx context.Context, tx *sql.Conn, stmt *sql.Stmt, columns []timestampColumn, values []any) (int64, bool, error) {
	if columns[0].table == "sessions" {
		var revision int64
		if err := stmt.QueryRowContext(ctx, values...).Scan(&revision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, false, nil
			}
			return 0, false, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE sessions SET revision=? WHERE rowid=? AND revision<>?`, revision, values[len(columns)], revision)
		if err != nil {
			return 0, false, err
		}
		n, err := result.RowsAffected()
		return 1 + 2*n, true, err
	}
	result, err := stmt.ExecContext(ctx, values...)
	if err != nil {
		return 0, false, err
	}
	n, err := result.RowsAffected()
	return n, n > 0, err
}

func legacyTimestampPredicate(column string) string {
	return fmt.Sprintf(`typeof(%[1]s)='text'
AND ((%[1]s GLOB '* [+-][0-9][0-9][0-9][0-9] *' AND %[1]s NOT GLOB '* +0000 UTC')
OR %[1]s GLOB '????-??-??T*'
OR %[1]s GLOB '????-??-?? ??:??:*[+-]??:??'
OR %[1]s GLOB '????-??-?? ??:??:*Z'
OR %[1]s GLOB '????-??-?? ??:??'
OR %[1]s GLOB '????-??-?? ??:??Z'
OR %[1]s GLOB '????-??-??Z')`, quoteIdentifier(column))
}

// A covering index avoids loading transcript/command payloads just to discover
// that their timestamps are already UTC. Bounds keep repair linear even when
// an entire old database needs rewriting; only two row IDs stay in memory.
func timestampRepairBounds(ctx context.Context, db *sql.DB, columns []timestampColumn) (int64, int64, bool, error) {
	args := make([]any, 0, len(columns)+2)
	args = append(args, columns[0].table)
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
	query := strings.Join([]string{"SELECT min(rowid),max(rowid) FROM ", quoteIdentifier(columns[0].table), " INDEXED BY ", quoteIdentifier(index), " WHERE ", strings.Join(predicates, " OR ")}, "")
	err = db.QueryRowContext(ctx, query).Scan(&first, &last)
	return first.Int64, last.Int64, !first.Valid, err
}

type timestampBindingDrop struct {
	session, provider, keptAccount, droppedAccount string
	rowid                                          int64
}

// Resolve only collisions whose logical winner is known. Never replace a run
// carrying execution history with a freshly materialized pending occurrence.
func resolveTimestampCollision(ctx context.Context, tx *sql.Conn, columns []timestampColumn, values []any) (*timestampBindingDrop, error) {
	var binding *timestampBindingDrop
	table := columns[0].table
	id, ok := values[len(columns)].(int64)
	if !ok {
		return nil, errors.New("timestamp collision has an invalid row identity")
	}
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
		binding = &timestampBindingDrop{}
		if err := tx.QueryRowContext(ctx, `SELECT twin.rowid,original.session_id,original.provider,original.account_id,twin.account_id
 FROM session_agent_accounts original JOIN session_agent_accounts twin
 ON twin.session_id=original.session_id AND twin.provider=original.provider AND twin.bound_at=?
 WHERE original.rowid=? AND twin.rowid<>original.rowid`, at, id).Scan(&twin, &binding.session, &binding.provider, &binding.droppedAccount, &binding.keptAccount); err != nil {
			return nil, err
		}
		// The last inserted same-instant binding is the effective bind.
		if id > twin {
			id = twin
			binding.keptAccount, binding.droppedAccount = binding.droppedAccount, binding.keptAccount
		}
		binding.rowid = id
	case "automation_runs":
		if err := tx.QueryRowContext(ctx, `SELECT twin.rowid FROM automation_runs original JOIN automation_runs twin
 ON twin.automation_id=original.automation_id AND twin.scheduled_for=?
 WHERE original.rowid=? AND twin.rowid<>original.rowid`, at, id).Scan(&twin); err != nil {
			return nil, err
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
			return nil, err
		}
		twinEmpty, err := empty(twin)
		if err != nil {
			return nil, err
		}
		if !originalEmpty && !twinEmpty {
			var automation string
			if err := tx.QueryRowContext(ctx, `SELECT automation_id FROM automation_runs WHERE rowid=?`, id).Scan(&automation); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("automation %s instant %v has two execution histories (rowids %v and %d): %w", automation, at, id, twin, errTimestampPolicySkip)
		}
		if !originalEmpty {
			id = twin
		}
	default:
		return nil, fmt.Errorf("unhandled timestamp key collision in %s", table)
	}
	var deletion strings.Builder
	deletion.WriteString("DELETE FROM ")
	deletion.WriteString(quoteIdentifier(table))
	deletion.WriteString(" WHERE rowid=?")
	_, err := tx.ExecContext(ctx, deletion.String(), id)
	return binding, err
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
