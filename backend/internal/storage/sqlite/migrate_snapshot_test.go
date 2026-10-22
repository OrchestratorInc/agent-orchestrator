package sqlite

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
)

// migrationSnapshots caches a database image for every goose version the
// package's tests start from. Under -race a single full migration replay costs
// ~12s, and the suite has ~80 tests that each rebuilt their starting schema
// from scratch, so the package spent ~15 minutes replaying migrations. Images
// are built once, in ascending order, one migration at a time: the package now
// pays for one replay no matter how many versions the tests ask for.
var migrationSnapshots struct {
	sync.Mutex
	images  map[int64][]byte
	highest int64
}

// snapshotDSN keeps the checkpoint databases in rollback-journal mode so ao.db
// is complete on disk after every commit. WAL would leave committed frames in a
// sidecar file that a plain file read silently misses.
const snapshotDSN = "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"

// noForeignKeysDSN is for tests that seed rows whose parents do not exist yet.
const noForeignKeysDSN = "?_pragma=busy_timeout(5000)"

// migratedDatabaseSnapshot returns a copy of a database migrated through
// version. Versions below the highest one built are served from the cache, so
// requests may arrive in any order.
func migratedDatabaseSnapshot(t *testing.T, version int64) []byte {
	t.Helper()
	migrationSnapshots.Lock()
	defer migrationSnapshots.Unlock()
	if snapshot, ok := migrationSnapshots.images[version]; ok {
		return append([]byte(nil), snapshot...)
	}
	return append([]byte(nil), buildMigrationSnapshots(t, version, migrationsFS)...)
}

func buildMigrationSnapshots(t *testing.T, version int64, fsys fs.FS) []byte {
	t.Helper()
	all, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("collect migration checkpoints through %d: %v", version, err)
	}
	var migrations []migrationFile
	for _, m := range all {
		if m.version <= version {
			migrations = append(migrations, m)
		}
	}
	if migrationSnapshots.images == nil {
		migrationSnapshots.images = map[int64][]byte{}
	}
	// Missing IDs mean the preceding schema, including existing callers that
	// request latest-1 when the latest migration has a timestamp ID.
	if snapshot, ok := migrationSnapshots.images[migrations[len(migrations)-1].version]; ok {
		migrationSnapshots.images[version] = snapshot
		return snapshot
	}
	databasePath := filepath.Join(t.TempDir(), "ao.db")
	if migrationSnapshots.highest > 0 {
		if err := os.WriteFile(databasePath, migrationSnapshots.images[migrationSnapshots.highest], 0o600); err != nil {
			t.Fatalf("resume migration checkpoint %d: %v", migrationSnapshots.highest, err)
		}
	}
	db, err := sql.Open("sqlite", "file:"+databasePath+snapshotDSN)
	if err != nil {
		t.Fatalf("open migration checkpoint: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, migration := range migrations {
		next := migration.version
		if next <= migrationSnapshots.highest {
			continue
		}
		if err := migrateUpTo(db, fsys, next); err != nil {
			t.Fatalf("migrate to %d: %v", next, err)
		}
		snapshot, err := os.ReadFile(databasePath)
		if err != nil {
			t.Fatalf("read migration checkpoint %d: %v", next, err)
		}
		migrationSnapshots.images[next] = snapshot
		migrationSnapshots.highest = next
	}
	migrationSnapshots.images[version] = migrationSnapshots.images[migrationSnapshots.highest]
	return migrationSnapshots.images[version]
}

// openMigratedDatabaseCopy returns an isolated database migrated through
// version, opened with the package's production pragmas. Tests that exercise
// migration behaviour seed their legacy data here and then call migrate().
func openMigratedDatabaseCopy(t *testing.T, version int64) *sql.DB {
	t.Helper()
	return openMigratedDatabaseCopyAt(t, t.TempDir(), version, pragmas)
}

// openMigratedDatabaseCopyNoForeignKeys is for tests that seed rows whose
// parents do not exist yet.
func openMigratedDatabaseCopyNoForeignKeys(t *testing.T, version int64) *sql.DB {
	t.Helper()
	return openMigratedDatabaseCopyAt(t, t.TempDir(), version, noForeignKeysDSN)
}

// openMigratedDatabaseCopyAt clones the snapshot for version into dataDir. Use
// it when the test later hands that directory to the production Open path.
func openMigratedDatabaseCopyAt(t *testing.T, dataDir string, version int64, dsn string) *sql.DB {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dataDir, "ao.db"), migratedDatabaseSnapshot(t, version), 0o600); err != nil {
		t.Fatalf("copy migration %d checkpoint: %v", version, err)
	}
	db, err := sql.Open("sqlite", databaseURI(dataDir)+dsn)
	if err != nil {
		t.Fatalf("open migration %d copy: %v", version, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationSnapshotsWithSparseTimestampVersions(t *testing.T) {
	migrationSnapshots.Lock()
	defer migrationSnapshots.Unlock()
	images, highest := migrationSnapshots.images, migrationSnapshots.highest
	defer func() {
		migrationSnapshots.images, migrationSnapshots.highest = images, highest
	}()
	migrationSnapshots.images, migrationSnapshots.highest = nil, 0

	const firstTimestamp, lastTimestamp = int64(20261013093000123), int64(20261014093000123)
	fsys := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte(`-- +goose Up
CREATE TABLE checkpoints (version INTEGER PRIMARY KEY);
INSERT INTO checkpoints VALUES (1);
`)},
		"migrations/0003_legacy.sql": {Data: []byte(`-- +goose Up
INSERT INTO checkpoints VALUES (3);
`)},
		"migrations/20261013093000123_first.sql": {Data: []byte(`-- +goose Up
INSERT INTO checkpoints VALUES (20261013093000123);
`)},
		"migrations/20261014093000123_last.sql": {Data: []byte(`-- +goose Up
INSERT INTO checkpoints VALUES (20261014093000123);
`)},
	}
	for _, tt := range []struct {
		requested int64
		applied   int64
		rows      int
	}{
		{3, 3, 2},
		{22, 3, 2},
		{firstTimestamp, firstTimestamp, 3},
		{lastTimestamp, lastTimestamp, 4},
		{firstTimestamp - 1, 3, 2},
		{2, 1, 1},
		{firstTimestamp + 1, firstTimestamp, 3},
		{lastTimestamp - 1, firstTimestamp, 3},
		{lastTimestamp, lastTimestamp, 4},
	} {
		snapshot := buildMigrationSnapshots(t, tt.requested, fsys)
		databasePath := filepath.Join(t.TempDir(), "ao.db")
		if err := os.WriteFile(databasePath, snapshot, 0o600); err != nil {
			t.Fatalf("copy checkpoint %d: %v", tt.requested, err)
		}
		db, err := sql.Open("sqlite", "file:"+databasePath+snapshotDSN)
		if err != nil {
			t.Fatalf("open checkpoint %d: %v", tt.requested, err)
		}
		var applied int64
		var rows int
		err = db.QueryRow(`SELECT
			(SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1),
			(SELECT COUNT(*) FROM checkpoints)`).Scan(&applied, &rows)
		_ = db.Close()
		if err != nil {
			t.Fatalf("read checkpoint %d: %v", tt.requested, err)
		}
		if applied != tt.applied || rows != tt.rows {
			t.Fatalf("checkpoint %d: applied/rows = %d/%d, want %d/%d", tt.requested, applied, rows, tt.applied, tt.rows)
		}
	}
}

// TestMigrationSnapshotsMatchTheRequestedVersion guards the cache: a snapshot
// must record exactly the requested version, must be independent of the images
// it was built from, and must carry the schema that version really has.
func TestMigrationSnapshotsMatchTheRequestedVersion(t *testing.T) {
	latest, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}

	// Deliberately request a high version first so the lower ones are served
	// from the cache rather than rebuilt.
	for _, version := range []int64{latest, 12, 43, 103, latest} {
		db := openMigratedDatabaseCopy(t, version)
		var got int64
		if err := db.QueryRow(
			`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`,
		).Scan(&got); err != nil {
			t.Fatalf("read applied version at %d: %v", version, err)
		}
		if got != version {
			t.Fatalf("snapshot %d reports applied version %d", version, got)
		}
	}

	// A clone must not share schema, data, or ledger state with the snapshot it
	// came from or with a sibling clone.
	first := openMigratedDatabaseCopy(t, 103)
	if _, err := first.Exec(`
INSERT INTO projects (id, path, registered_at) VALUES ('clone-only', '/clone-only', CURRENT_TIMESTAMP);
CREATE TABLE clone_only (id INTEGER);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1);
`); err != nil {
		t.Fatalf("seed clone: %v", err)
	}
	second := openMigratedDatabaseCopy(t, 103)
	var leakedRows, leakedTables, leakedVersions int
	if err := second.QueryRow(`SELECT
		(SELECT COUNT(*) FROM projects WHERE id = 'clone-only'),
		(SELECT COUNT(*) FROM sqlite_master WHERE name = 'clone_only'),
		(SELECT COUNT(*) FROM goose_db_version WHERE version_id = 9999)`).Scan(&leakedRows, &leakedTables, &leakedVersions); err != nil {
		t.Fatalf("read second clone: %v", err)
	}
	if leakedRows != 0 || leakedTables != 0 || leakedVersions != 0 {
		t.Fatalf("clone leaked rows/tables/versions = %d/%d/%d into a sibling clone", leakedRows, leakedTables, leakedVersions)
	}
}
