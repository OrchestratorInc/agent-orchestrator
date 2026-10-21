package sqlite

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// offsetOnlyTimestampRepairVersion is the PRAGMA user_version that marks a
// database whose offset-only timestamps have been rewritten. Nothing else in the
// repository uses user_version.
const offsetOnlyTimestampRepairVersion = 1

// offsetOnlyTimestamp matches the text the driver writes for a time in a zone
// with no name: a hook or API caller sent "2026-10-21T18:15:40+02:00" and the
// daemon is not itself in that zone. The driver stores
// "2026-10-21 18:15:40.647754 +0200 +0200" and cannot scan it back into a
// time.Time, so every query that reads the row fails with "unsupported Scan,
// storing driver.Value type string into type *time.Time" (#7026). A zone with a
// name ("+0200 CEST"), UTC, and the monotonic-clock suffix (" m=+5.31") all read
// back fine and are left alone.
var offsetOnlyTimestamp = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?) ([+-]\d{4}) [+-]\d{4}(?: m=[+-][0-9.]+)?$`)

// repairOffsetOnlyTimestamps rewrites every offset-only timestamp, in any
// timestamp column of any table, as the same instant in UTC. Releases up to
// 0.14.4 wrote them from hook timestamps; the daemon then fails to list sessions
// (and 0.14.4 fails to boot). The pass is idempotent and runs once per database:
// scanning every timestamp column is too much work to repeat on each start.
func repairOffsetOnlyTimestamps(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version >= offsetOnlyTimestampRepairVersion {
		return nil
	}
	columns, err := timestampColumns(db)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin timestamp repair: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, column := range columns {
		if err := repairOffsetOnlyColumn(tx, column); err != nil {
			return err
		}
	}
	// PRAGMA takes no bound parameters.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, offsetOnlyTimestampRepairVersion)); err != nil {
		return fmt.Errorf("mark timestamp repair done: %w", err)
	}
	return tx.Commit()
}

type timestampColumn struct{ table, column string }

// timestampColumns lists the columns the driver decodes as times: the ones
// declared TIMESTAMP, DATETIME or DATE.
func timestampColumns(db *sql.DB) ([]timestampColumn, error) {
	rows, err := db.Query(`
SELECT m.name, p.name
FROM sqlite_master AS m, pragma_table_info(m.name) AS p
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.sql NOT LIKE 'CREATE VIRTUAL%'
  AND upper(p.type) IN ('TIMESTAMP', 'DATETIME', 'DATE')
ORDER BY m.name, p.cid`)
	if err != nil {
		return nil, fmt.Errorf("list timestamp columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var columns []timestampColumn
	for rows.Next() {
		var c timestampColumn
		if err := rows.Scan(&c.table, &c.column); err != nil {
			return nil, fmt.Errorf("scan timestamp column: %w", err)
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

func repairOffsetOnlyColumn(tx *sql.Tx, c timestampColumn) error {
	table, column := quoteIdentifier(c.table), quoteIdentifier(c.column)
	rows, err := tx.Query(fmt.Sprintf(
		`SELECT DISTINCT CAST(%[2]s AS TEXT) FROM %[1]s WHERE typeof(%[2]s) = 'text'
AND CAST(%[2]s AS TEXT) GLOB '*[+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]*'`, table, column))
	if err != nil {
		return fmt.Errorf("find offset-only timestamps in %s.%s: %w", c.table, c.column, err)
	}
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan %s.%s: %w", c.table, c.column, err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, value := range values {
		match := offsetOnlyTimestamp.FindStringSubmatch(value)
		if match == nil {
			continue
		}
		at, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", match[1]+" "+match[2])
		if err != nil {
			continue // not a real date; leave the value as it is
		}
		if _, err := tx.Exec(fmt.Sprintf(
			`UPDATE %[1]s SET %[2]s = ? WHERE typeof(%[2]s) = 'text' AND CAST(%[2]s AS TEXT) = ?`, table, column,
		), at.UTC(), value); err != nil {
			return fmt.Errorf("rewrite offset-only timestamp in %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
