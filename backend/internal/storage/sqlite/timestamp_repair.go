package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/timestamps"
)

// Kept for older builds; a completed pass is no proof an old writer has not
// subsequently stored more local times. Never use this marker to skip detection.
const offsetOnlyTimestampRepairVersion = 1
const timestampRepairBatch = 256

// Scan forward by row identity, with bounded memory and write transactions.
// Related timestamps change together so row CHECK constraints remain valid.
// SQL-side UTC defaults remain unchanged. Invalid dates are preserved.
func repairTimestamps(ctx context.Context, db *sql.DB) error {
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		return err
	}
	for i := 0; i < len(columns); {
		end := i + 1
		for end < len(columns) && columns[end].table == columns[i].table {
			end++
		}
		group := columns[i:end]
		i = end
		cursor := int64(math.MinInt64)
		for {
			next, done, err := repairTimestampBatch(ctx, db, group, cursor)
			if err != nil {
				return fmt.Errorf("repair %s: %w", group[0].table, err)
			}
			if done {
				break
			}
			cursor = next
		}
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
	table := quoteIdentifier(columns[0].table)
	fields, sets, checks := make([]string, len(columns)), make([]string, len(columns)), make([]string, len(columns))
	for i, c := range columns {
		column := quoteIdentifier(c.column)
		fields[i] = fmt.Sprintf(`CASE WHEN typeof(%[1]s)='text'
AND ((%[1]s GLOB '* [+-][0-9][0-9][0-9][0-9] *' AND %[1]s NOT GLOB '* +0000 UTC')
OR %[1]s GLOB '????-??-??T*') THEN CAST(%[1]s AS TEXT) END`, column)
		sets[i] = fmt.Sprintf("%s=coalesce(?,%s)", column, column)
		checks[i] = fmt.Sprintf("(? IS NULL OR (typeof(%[1]s)='text' AND CAST(%[1]s AS TEXT)=?))", column)
	}
	// Clean rows also consume the scan budget, so detection cannot hold the
	// sole writer connection for an entire clean or sparsely affected table.
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT rowid,%s FROM %s WHERE rowid>=? ORDER BY rowid LIMIT ?`, strings.Join(fields, ","), table), cursor, timestampRepairBatch)
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
		stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE rowid=? AND %s`, table, strings.Join(sets, ","), strings.Join(checks, " AND ")))
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = stmt.Close() }()
		for _, v := range changes {
			// A concurrent writer may have replaced this value since detection.
			if _, err = stmt.ExecContext(ctx, v...); err != nil {
				return 0, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
	}
	return last + 1, n < timestampRepairBatch || last == math.MaxInt64, nil
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
