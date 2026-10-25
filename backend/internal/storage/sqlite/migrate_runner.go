package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// In-repo migration runner replacing pressly/goose (removed 2026-10-20).
// goose's ledger-replay semantics caused every daemon-wedging migration
// collision in the fork (9011 gateway_entries 2026-10-14, 9012 review
// publication 2026-10-20), and with upstream syncs frozen the renumbering
// churn its out-of-order machinery existed for is gone. The ledger table
// goose_db_version keeps its name and schema; a migration counts as applied
// iff its latest ledger row has is_applied = 1.

const migrationsDir = "migrations"

const migrationLedgerDDL = `CREATE TABLE IF NOT EXISTS goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now'))
	)`

type migrationFile struct {
	version int64
	name    string
	upSQL   string
	downSQL string
}

// migrationFileNameVersion extracts the numeric prefix of a migration
// filename ("9012_review_run_publication.sql" → 9012). Same contract as the
// goose.NumericComponent the migration tests previously relied on.
func migrationFileNameVersion(filename string) (int64, error) {
	stem := strings.TrimSuffix(filename, path.Ext(filename))
	prefix, _, found := strings.Cut(stem, "_")
	if !found {
		return 0, fmt.Errorf("migration filename %q has no numeric prefix", filename)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration filename %q has a non-numeric prefix: %w", filename, err)
	}
	return version, nil
}

// splitMigrationSQL partitions a migration file body on the "-- +goose Up" /
// "-- +goose Down" markers. A file without markers is entirely Up.
func splitMigrationSQL(body string) (up, down string) {
	var upLines, downLines []string
	section := 0 // 0 = before any marker, 1 = Up, 2 = Down
	sawMarker := false
	for _, line := range strings.Split(body, "\n") {
		switch strings.TrimSpace(line) {
		case "-- +goose Up":
			section, sawMarker = 1, true
			continue
		case "-- +goose Down":
			section, sawMarker = 2, true
			continue
		}
		switch section {
		case 0, 1:
			upLines = append(upLines, line)
		case 2:
			downLines = append(downLines, line)
		}
	}
	if !sawMarker {
		return body, ""
	}
	return strings.Join(upLines, "\n"), strings.Join(downLines, "\n")
}

func loadMigrations(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}
	var migrations []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationFileNameVersion(entry.Name())
		if err != nil {
			return nil, err
		}
		body, err := fs.ReadFile(fsys, path.Join(migrationsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		upSQL, downSQL := splitMigrationSQL(string(body))
		migrations = append(migrations, migrationFile{
			version: version,
			name:    entry.Name(),
			upSQL:   upSQL,
			downSQL: downSQL,
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// appliedMigrations returns the applied set: the latest ledger row per
// version wins, matching how the prepare*/repair* helpers read the ledger.
func appliedMigrations(db *sql.DB) (map[int64]bool, error) {
	rows, err := db.Query(`SELECT version_id, is_applied FROM goose_db_version ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		var isApplied int
		if err := rows.Scan(&version, &isApplied); err != nil {
			return nil, err
		}
		applied[version] = isApplied == 1
	}
	return applied, rows.Err()
}

func applyMigration(db *sql.DB, m migrationFile, up bool) error {
	body := m.downSQL
	isApplied := 0
	if up {
		body, isApplied = m.upSQL, 1
	}
	type execer interface {
		Exec(query string, args ...any) (sql.Result, error)
	}
	run := func(e execer) error {
		if strings.TrimSpace(body) != "" {
			if _, err := e.Exec(body); err != nil {
				return err
			}
		}
		_, err := e.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, ?)`, m.version, isApplied,
		)
		return err
	}
	// Files that manage their own transactions (explicit BEGIN, or the goose
	// NO TRANSACTION opt-out) must run without a wrapping transaction.
	if migrationManagesOwnTransaction(body) {
		return run(db)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := run(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func migrationManagesOwnTransaction(body string) bool {
	if strings.Contains(body, "-- +goose NO TRANSACTION") {
		return true
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(trimmed, "BEGIN") || strings.HasPrefix(trimmed, "START TRANSACTION") {
			return true
		}
	}
	return false
}

// migrateUpTo applies every not-yet-applied migration with version <= to, in
// ascending order. There is no out-of-order guard: a fork-range migration
// (9000+) may legitimately be missing while later upstream files applied.
func migrateUpTo(db *sql.DB, fsys fs.FS, to int64) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := db.Exec(migrationLedgerDDL); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}
	applied, err := appliedMigrations(db)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version > to {
			break
		}
		if applied[m.version] {
			continue
		}
		present, err := migrationEffectsPresent(db, m)
		if err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if present {
			// Every effect the file would create already exists: the file is a
			// renumbered re-introduction of schema an earlier install applied
			// under the migration's previous version. Executing it could only
			// fail, so record the new version and skip. Empty Up sections take
			// the same path applyMigration would give them.
			if _, err := db.Exec(
				`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, ?)`, m.version, 1,
			); err != nil {
				return fmt.Errorf("migration %s: %w", m.name, err)
			}
			continue
		}
		if err := applyMigration(db, m, true); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// migrationEffectsPresent reports whether every statement of the migration's
// Up section is a plain ALTER TABLE ADD COLUMN, CREATE TABLE, or CREATE
// [UNIQUE] INDEX whose target the database already holds. That is the
// signature of this fork's recurring outage: a sync renumbered a fork-local
// migration onto a fresh version after a live install applied it under the
// old one (9011 gateway_entries, 9012 review publication,
// 20261022172135182 report_emitted_at, 20261022172135183 review_run_current_
// head_lookup), and replaying the file could only die on "duplicate column
// name", "table already exists", or "index already exists".
//
// Files containing any other statement kind return false and run normally:
// skipping a mixed file could silently drop a backfill or data migration the
// database never received, so those stay loud and remain the territory of
// hand-written prepare*/repair* helpers.
//
// ponytail: naive ";" split and prefix matching — quoted ";" inside these DDL
// kinds does not occur in this repo's migrations, and anything that doesn't
// parse as one of the patterns falls out as "run normally", which is the
// pre-guard behavior.
func migrationEffectsPresent(db *sql.DB, m migrationFile) (bool, error) {
	for _, raw := range strings.Split(m.upSQL, ";") {
		stmt := stripSQLCommentLines(raw)
		if stmt == "" {
			continue
		}
		check, ok := parsePresenceCheck(stmt)
		if !ok {
			return false, nil
		}
		var found int
		var err error
		switch check.kind {
		case "column":
			err = db.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ? COLLATE NOCASE`, check.table, check.name,
			).Scan(&found)
		case "index":
			err = db.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ? COLLATE NOCASE`, check.name,
			).Scan(&found)
		default:
			err = db.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ? COLLATE NOCASE`, check.name,
			).Scan(&found)
		}
		if err != nil {
			return false, err
		}
		if found == 0 {
			return false, nil
		}
	}
	return true, nil
}

// stripSQLCommentLines drops "--" comment lines and surrounding whitespace so
// a statement carrying annotation comments still matches the presence
// patterns.
func stripSQLCommentLines(raw string) string {
	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

// presenceCheck describes one guarded DDL statement: what to look up and
// where. kind is "column" (name within table), "index", or "table" (name).
type presenceCheck struct {
	kind  string
	name  string
	table string
}

// parsePresenceCheck recognizes the replay-unsafe DDL kinds the guard skips —
// "ALTER TABLE <t> ADD [COLUMN] <c>", "CREATE TABLE [IF NOT EXISTS] <t>", and
// "CREATE [UNIQUE] INDEX [IF NOT EXISTS] <i> ON <t>" — returning what to
// check. ok=false for any other statement shape.
func parsePresenceCheck(stmt string) (presenceCheck, bool) {
	upper := strings.ToUpper(stmt)
	switch {
	case strings.HasPrefix(upper, "ALTER TABLE "):
		rest := stmt[len("ALTER TABLE "):]
		table, restAfter := nextIdentifier(rest)
		if table == "" {
			return presenceCheck{}, false
		}
		restAfter = strings.TrimSpace(restAfter)
		if !strings.HasPrefix(strings.ToUpper(restAfter), "ADD ") {
			return presenceCheck{}, false
		}
		restAfter = restAfter[len("ADD "):]
		if strings.HasPrefix(strings.ToUpper(restAfter), "COLUMN ") {
			restAfter = restAfter[len("COLUMN "):]
		}
		column, _ := nextIdentifier(restAfter)
		if column == "" {
			return presenceCheck{}, false
		}
		return presenceCheck{kind: "column", name: column, table: table}, true
	case strings.HasPrefix(upper, "CREATE TABLE "):
		rest := stmt[len("CREATE TABLE "):]
		if strings.HasPrefix(strings.ToUpper(rest), "IF NOT EXISTS ") {
			rest = rest[len("IF NOT EXISTS "):]
		}
		table, _ := nextIdentifier(rest)
		if table == "" {
			return presenceCheck{}, false
		}
		return presenceCheck{kind: "table", name: table}, true
	case strings.HasPrefix(upper, "CREATE UNIQUE INDEX "), strings.HasPrefix(upper, "CREATE INDEX "):
		rest := stmt[strings.Index(upper, "INDEX ")+len("INDEX "):]
		if strings.HasPrefix(strings.ToUpper(rest), "IF NOT EXISTS ") {
			rest = rest[len("IF NOT EXISTS "):]
		}
		index, _ := nextIdentifier(rest)
		if index == "" {
			return presenceCheck{}, false
		}
		return presenceCheck{kind: "index", name: index}, true
	}
	return presenceCheck{}, false
}

// nextIdentifier extracts a leading SQL identifier — plain, double-quoted,
// backquoted, or bracketed — and returns the remainder of the string.
func nextIdentifier(s string) (ident, rest string) {
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" {
		return "", ""
	}
	switch s[0] {
	case '"', '`':
		if i := strings.IndexByte(s[1:], s[0]); i > 0 {
			return s[1 : 1+i], s[2+i:]
		}
		return "", ""
	case '[':
		if i := strings.IndexByte(s, ']'); i > 0 {
			return s[1:i], s[i+1:]
		}
		return "", ""
	}
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		isWord := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'
		if !isWord {
			end = i
			break
		}
	}
	if end == 0 {
		return "", ""
	}
	return s[:end], s[end:]
}

// migrateDownTo reverts every applied migration with version > to, in
// descending order.
func migrateDownTo(db *sql.DB, fsys fs.FS, to int64) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := db.Exec(migrationLedgerDDL); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}
	applied, err := appliedMigrations(db)
	if err != nil {
		return err
	}
	for i := len(migrations) - 1; i >= 0; i-- {
		m := migrations[i]
		if m.version <= to {
			break
		}
		if !applied[m.version] {
			continue
		}
		if err := applyMigration(db, m, false); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}
