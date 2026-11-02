package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// Kept for older builds; a completed pass is no proof an old writer has not
// subsequently stored more local times. Never use this marker to skip detection.
const offsetOnlyTimestampRepairVersion = 1
const timestampRepairBatch = 256

var legacyDriverTimestamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) ([+-]\d{4}) \S+(?: m=[+-][0-9.]+)?$`)

func legacyTimestamp(value string) (time.Time, bool) {
	if match := legacyDriverTimestamp.FindStringSubmatch(value); match != nil {
		at, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", match[1]+" "+match[2])
		return at.UTC(), err == nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	return at.UTC(), err == nil
}

func repairOffsetOnlyTimestamps(db *sql.DB) error {
	return repairTimestamps(context.Background(), db)
}

// Scan forward by row identity, with bounded memory and write transactions.
// Each row is visited once per column, regardless of timestamp cardinality.
// SQL-side UTC defaults remain unchanged. Invalid dates are preserved.
func repairTimestamps(ctx context.Context, db *sql.DB) error {
	columns, err := timestampColumns(ctx, db)
	if err != nil {
		return err
	}
	for _, c := range columns {
		cursor := int64(math.MinInt64)
		for {
			next, done, err := repairTimestampBatch(ctx, db, c, cursor)
			if err != nil {
				return fmt.Errorf("repair %s.%s: %w", c.table, c.column, err)
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

func repairTimestampBatch(ctx context.Context, db *sql.DB, c timestampColumn, cursor int64) (int64, bool, error) {
	table, column := quoteIdentifier(c.table), quoteIdentifier(c.column)
	// The range is indexed by rowid; LIMIT bounds results even for distinct times.
	// Exclude canonical UTC before parsing, so clean databases only do detection.
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT rowid,CAST(%[2]s AS TEXT) FROM %[1]s
WHERE rowid>=? AND typeof(%[2]s)='text'
AND ((%[2]s GLOB '* [+-][0-9][0-9][0-9][0-9] *' AND %[2]s NOT GLOB '* +0000 UTC')
OR %[2]s GLOB '????-??-??T*') ORDER BY rowid LIMIT ?`, table, column), cursor, timestampRepairBatch)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	type change struct {
		id     int64
		before string
		after  time.Time
	}
	changes := make([]change, 0, timestampRepairBatch)
	n, last := 0, cursor
	for rows.Next() {
		var id int64
		var value string
		if err = rows.Scan(&id, &value); err != nil {
			break
		}
		n++
		last = id
		if at, ok := legacyTimestamp(value); ok && at.String() != value {
			changes = append(changes, change{id, value, at})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return 0, false, err
	}
	if len(changes) > 0 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = tx.Rollback() }()
		stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`UPDATE %[1]s SET %[2]s=? WHERE rowid=? AND CAST(%[2]s AS TEXT)=?`, table, column))
		if err != nil {
			return 0, false, err
		}
		defer func() { _ = stmt.Close() }()
		for _, v := range changes {
			// A concurrent writer may have replaced this value since detection.
			if _, err = stmt.ExecContext(ctx, v.after, v.id, v.before); err != nil {
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
