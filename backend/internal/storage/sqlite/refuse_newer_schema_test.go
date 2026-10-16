package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// ledgerDataDir returns a data dir whose ao.db is migrated to exactly version,
// with extra applied rows appended to its goose ledger. The seeding handle is
// closed, so the directory can go straight to Open.
func ledgerDataDir(t *testing.T, version int64, extra ...int64) string {
	t.Helper()
	dataDir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dataDir, version, snapshotDSN)
	seedLedger(t, db, extra...)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
	return dataDir
}

func seedLedger(t *testing.T, db *sql.DB, versions ...int64) {
	t.Helper()
	for _, version := range versions {
		if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, version); err != nil {
			t.Fatalf("seed ledger version %d: %v", version, err)
		}
	}
}

// hibernationPreviewDataDir reproduces a development database from a build that
// numbered session hibernation 0179-0181: the 0190 schema is present and the
// ledger records the old number. extra versions are appended to the ledger.
func hibernationPreviewDataDir(t *testing.T, legacyVersion int64, extra ...int64) string {
	t.Helper()
	body, err := migrationsFS.ReadFile("migrations/0190_session_hibernation.sql")
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dataDir, legacyVersion-1, snapshotDSN)
	if _, err := db.Exec(strings.Split(string(body), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	seedLedger(t, db, append([]int64{legacyVersion}, extra...)...)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}
	return dataDir
}

func readOnlyDatabase(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(dataDir)+readOnlyPragmas)
	if err != nil {
		t.Fatalf("open database read-only: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readLedger returns every goose ledger row, in insertion order.
func readLedger(t *testing.T, dataDir string) []string {
	t.Helper()
	rows, err := readOnlyDatabase(t, dataDir).Query(`SELECT id, version_id, is_applied FROM goose_db_version ORDER BY id`)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ledger []string
	for rows.Next() {
		var id, version, applied int64
		if err := rows.Scan(&id, &version, &applied); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		ledger = append(ledger, fmt.Sprintf("%d:%d:%d", id, version, applied))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return ledger
}

func appliedVersions(t *testing.T, dataDir string) map[int64]bool {
	t.Helper()
	rows, err := readOnlyDatabase(t, dataDir).Query(`SELECT DISTINCT version_id FROM goose_db_version WHERE is_applied = 1`)
	if err != nil {
		t.Fatalf("read applied versions: %v", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan applied version: %v", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read applied versions: %v", err)
	}
	return applied
}

func embeddedHead(t *testing.T) int64 {
	t.Helper()
	head, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	return head
}

// embeddedGaps lists the version numbers below head that no embedded migration
// file claims: burned, foreign-build and renumbered-branch versions that real
// ledgers still record.
func embeddedGaps(t *testing.T, head int64) []int64 {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	present := map[int64]bool{}
	for _, entry := range entries {
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			t.Fatalf("migration %q has no version goose can parse: %v", entry.Name(), err)
		}
		present[version] = true
	}
	var gaps []int64
	for version := int64(1); version < head; version++ {
		if !present[version] {
			gaps = append(gaps, version)
		}
	}
	return gaps
}

func closeStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func TestOpenRefusesDatabaseMigratedByNewerAO(t *testing.T) {
	head := embeddedHead(t)
	dataDir := ledgerDataDir(t, head, head+1)

	store, err := Open(dataDir)
	if err == nil {
		closeStore(t, store)
		t.Fatal("Open succeeded on a database a newer AO migrated; want ErrSchemaNewer")
	}
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("Open error = %v, want ErrSchemaNewer", err)
	}
	for _, want := range []string{
		filepath.Join(dataDir, "ao.db"),
		fmt.Sprintf("migration %04d", head+1),
		fmt.Sprintf("up to %04d", head),
		"update AO",
		"AO_DATA_DIR",
		"AO_ALLOW_NEWER_SCHEMA=1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Open error %q does not mention %q", err, want)
		}
	}
}

// A refused database must be left exactly as found. The renumbering repairs
// write to the goose ledger, so the refusal has to come before them.
func TestOpenRefusesNewerDatabaseBeforeRepairingLegacyHistory(t *testing.T) {
	head := embeddedHead(t)
	dataDir := hibernationPreviewDataDir(t, 179, head+1)
	before := readLedger(t, dataDir)

	store, err := Open(dataDir)
	if err == nil {
		closeStore(t, store)
		t.Fatal("Open succeeded on a legacy database a newer AO also migrated; want ErrSchemaNewer")
	}
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("Open error = %v, want ErrSchemaNewer", err)
	}
	if after := readLedger(t, dataDir); !slices.Equal(before, after) {
		t.Fatalf("refused Open rewrote the migration ledger:\nbefore %v\nafter  %v", before, after)
	}
}

func TestOpenAcceptsDatabaseAtTheEmbeddedHead(t *testing.T) {
	dataDir := ledgerDataDir(t, embeddedHead(t))

	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open database at the embedded head: %v", err)
	}
	closeStore(t, store)
}

func TestOpenMigratesOlderDatabaseToTheEmbeddedHead(t *testing.T) {
	head := embeddedHead(t)
	dataDir := ledgerDataDir(t, head-1)

	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open database one migration behind: %v", err)
	}
	closeStore(t, store)
	if !appliedVersions(t, dataDir)[head] {
		t.Fatalf("migration %d was not applied to the older database", head)
	}
}

// Ledgers carry versions this build does not embed but that are not newer than
// it: burned (22), written by a foreign build (45, 46, 50, 51), renumbered
// branch migrations (55-65, 182-184). The repairs and WithAllowMissing handle
// them, so refusing them would strand real installs.
func TestOpenIgnoresUnknownVersionsBelowTheEmbeddedHead(t *testing.T) {
	head := embeddedHead(t)
	gaps := embeddedGaps(t, head)
	if len(gaps) == 0 {
		t.Fatal("no embedded version gaps found; the burned-version ledger guarantees some, so the test would prove nothing")
	}
	dataDir := ledgerDataDir(t, head, gaps...)

	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open database recording unknown versions %v below the head: %v", gaps, err)
	}
	closeStore(t, store)
}

func TestOpenRepairsRenumberedHibernationHistory(t *testing.T) {
	for _, legacy := range []int64{179, 180, 181} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			dataDir := hibernationPreviewDataDir(t, legacy)

			store, err := Open(dataDir)
			if err != nil {
				t.Fatalf("Open database from a build that numbered hibernation %d: %v", legacy, err)
			}
			closeStore(t, store)
			applied := appliedVersions(t, dataDir)
			for _, version := range []int64{179, 180, 181, 190} {
				if !applied[version] {
					t.Errorf("migration %d is not applied after the renumbering repair", version)
				}
			}
		})
	}
}

// WithAllowMissing: a database that advanced past migrations added or
// renumbered later still receives them.
func TestOpenAppliesMissingMigrationsBelowARecordedVersion(t *testing.T) {
	dataDir := ledgerDataDir(t, 27, 46)

	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open database with missing earlier migrations: %v", err)
	}
	closeStore(t, store)
	applied := appliedVersions(t, dataDir)
	for _, version := range []int64{28, 29, 30, 31, embeddedHead(t)} {
		if !applied[version] {
			t.Errorf("migration %d was not applied", version)
		}
	}
}

// goose records a rolled-back migration as a later row with is_applied = 0.
func TestOpenIgnoresRolledBackNewerVersion(t *testing.T) {
	head := embeddedHead(t)
	dataDir := ledgerDataDir(t, head, head+1)
	db, err := sql.Open("sqlite", databaseURI(dataDir)+snapshotDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 0)`, head+1); err != nil {
		t.Fatalf("record rollback: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open database whose newer migration was rolled back: %v", err)
	}
	closeStore(t, store)
}

func TestOpenWithSchemaPolicyAllowsANewerDatabase(t *testing.T) {
	head := embeddedHead(t)
	dataDir := ledgerDataDir(t, head, head+1)

	store, err := OpenWithSchemaPolicy(dataDir, true)
	if err != nil {
		t.Fatalf("OpenWithSchemaPolicy(allowNewerSchema) on a newer database: %v", err)
	}
	closeStore(t, store)
	if !appliedVersions(t, dataDir)[head+1] {
		t.Fatalf("the newer migration %d vanished from the ledger", head+1)
	}
}
