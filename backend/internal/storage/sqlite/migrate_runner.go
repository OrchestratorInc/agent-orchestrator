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
		if err := applyMigration(db, m, true); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
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
