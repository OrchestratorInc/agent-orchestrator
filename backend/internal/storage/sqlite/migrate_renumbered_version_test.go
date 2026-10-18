package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrateRepairsRenumberedVersionAppliedByAnotherFile covers the failure a
// downstream branch produces every time it renumbers one of its own migrations
// past an upstream one. The branch's migration ships first as version N and is
// recorded in goose_db_version on live installs; a later rebase renumbers it
// and an upstream migration takes over N. Goose sees N applied and skips the
// upstream file forever, so its schema change never lands.
//
// Observed as: daemon boot dying with
// "list all sessions: SQL logic error: no such column: model (1)" — version 100
// applied, sessions.model absent, because 0100 had been add_user_config before
// it became session_model.
//
// migrate() must reconcile that history: after it runs, every column the
// embedded migrations declare has to exist, no matter which file once owned the
// version number.
func TestMigrateRepairsRenumberedVersionAppliedByAnotherFile(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Schema as it stood before the contested number: 0100_session_model.sql has
	// not run, so sessions has no model column.
	upTo(t, db, 99)
	if columnExists(t, db, "sessions", "model") {
		t.Fatal("precondition: sessions.model must not exist at version 99")
	}

	// The branch's own migration burned 100 on this install before the rebase
	// renumbered it; goose will now skip 0100_session_model.sql.
	if _, err := db.Exec(
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (100, 1)`,
	); err != nil {
		t.Fatalf("seed the contested version as applied: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate over a renumbered version: %v", err)
	}

	if !columnExists(t, db, "sessions", "model") {
		t.Error("sessions.model is still missing after migrate: version 100 was " +
			"recorded by a different file, so 0100_session_model.sql was skipped " +
			"and the daemon dies on 'no such column: model'")
	}

	// The daemon's very first query on boot. Assert it directly so a repair that
	// leaves the column unusable cannot pass.
	if _, err := db.Exec(`SELECT model FROM sessions LIMIT 1`); err != nil {
		t.Errorf("session list query still fails after migrate: %v", err)
	}
}

// burnedByForkRenumbering lists the version numbers a fork-local migration has
// actually occupied at some point in this fork's history, recovered from
//
//	git log --all --diff-filter=A --name-only -- .../migrations/
//
// Only these numbers are a real hazard: an install may carry one of them in
// goose_db_version from the fork migration that held it, so the upstream
// migration that inherits the number is skipped. Numbers upstream has never
// contested cannot strand anything, so testing them would assert a threat that
// does not exist.
//
// Now that fork migrations are pinned above forkReservedFloor, this list is
// closed — no new entry should ever be needed. If one is, the reservation was
// bypassed.
var burnedByForkRenumbering = map[int64]bool{
	28: true, 32: true, 37: true, 41: true, // add_user_config
	89: true, 95: true, 96: true, 99: true, // add_user_config / normalize_activity_last_at
	100: true, 101: true, 102: true, 103: true, 104: true, 105: true,
}

// TestForkBurnedVersionsAreRepairable is the general form of the bug that took
// down a live daemon: version 100 had been held by a fork migration, so
// 0100_session_model.sql was skipped and sessions.model never appeared.
//
// For every column an upstream migration adds at a number the fork has burned,
// simulate that history — mark the version applied without running it — and
// require migrate() to produce the column anyway. A missing schemaRepairs entry
// fails here rather than at a user's boot.
func TestForkBurnedVersionsAreRepairable(t *testing.T) {
	for _, want := range declaredColumns(t) {
		if !burnedByForkRenumbering[want.version] {
			continue
		}
		t.Run(want.table+"."+want.column, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })

			// Bring the schema to just before the migration that adds the column,
			// then burn its version the way a renumbered branch migration would.
			upTo(t, db, want.version-1)
			if !tableExists(t, db, want.table) {
				t.Skipf("%s does not exist yet at version %d", want.table, want.version-1)
			}
			if columnExists(t, db, want.table, want.column) {
				t.Skipf("%s.%s already present before version %d", want.table, want.column, want.version)
			}
			if _, err := db.Exec(
				`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, want.version,
			); err != nil {
				t.Fatalf("burn version %d: %v", want.version, err)
			}

			if err := migrate(db); err != nil {
				t.Fatalf("migrate with version %d burned: %v", want.version, err)
			}
			if !columnExists(t, db, want.table, want.column) {
				t.Errorf("%s.%s (declared by %s) is missing after migrate when version %d "+
					"was recorded by another file. A rebase that renumbers a branch migration "+
					"onto %d strands this column on every existing install; add a schemaRepairs "+
					"entry for it.",
					want.table, want.column, want.file, want.version, want.version)
			}
		})
	}
}

type declaredColumn struct {
	table, column, file string
	version             int64
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&n); err != nil {
		t.Fatalf("look up table %s: %v", table, err)
	}
	return n > 0
}

// declaredColumns parses "ALTER TABLE <t> ADD COLUMN <c>" out of the embedded
// migrations. Tables dropped or renamed by a later migration are skipped by the
// caller via columnExists returning false only for tables that still exist.
func declaredColumns(t *testing.T) []declaredColumn {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var out []declaredColumn
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(e.Name())
		if err != nil {
			t.Fatalf("migration %q has no version goose can parse: %v", e.Name(), err)
		}
		body, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", e.Name(), err)
		}
		// Only the Up section declares columns the live schema must carry.
		up, _, _ := strings.Cut(string(body), "-- +goose Down")
		for _, line := range strings.Split(up, "\n") {
			fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(line), ";"))
			// ALTER TABLE <table> ADD COLUMN <column> ...
			if len(fields) < 6 || !strings.EqualFold(fields[0], "ALTER") ||
				!strings.EqualFold(fields[1], "TABLE") ||
				!strings.EqualFold(fields[3], "ADD") ||
				!strings.EqualFold(fields[4], "COLUMN") {
				continue
			}
			out = append(out, declaredColumn{table: fields[2], column: fields[5], file: e.Name(), version: version})
		}
	}
	if len(out) == 0 {
		t.Fatal("parsed no ADD COLUMN statements: the parser stopped matching the migrations")
	}
	return out
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns of %s: %v", table, err)
	}
	return false
}
