package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func latestMigration(t *testing.T) int64 {
	t.Helper()
	version, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("expected migration version: %v", err)
	}
	return version
}

// snapshotFileFor spells out the on-disk name on purpose: it is what a person
// looks for when recovering, so a rename must fail a test.
func snapshotFileFor(dir string, from, to int64) string {
	return filepath.Join(dir, "backups", fmt.Sprintf("ao-pre-%04d-to-%04d.db", from, to))
}

// oldDatabase puts ao.db at version into dir, adds one project row per id, and
// returns the writer. The rows are committed but, until a checkpoint, live only
// in the WAL.
func oldDatabase(t *testing.T, dir string, version int64, projects ...string) *sql.DB {
	t.Helper()
	db := openMigratedDatabaseCopyAt(t, dir, version, pragmas)
	for _, id := range projects {
		addProject(t, db, id)
	}
	return db
}

func addProject(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, id, "/"+id)
}

// openFrozen reads a database file exactly as it lies on disk. immutable makes
// SQLite ignore any -wal beside it, so a row that only ever lived in the WAL is
// missing from what this connection sees.
func openFrozen(t *testing.T, path string) *sql.DB {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no database to read: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatalf("open %s: %v", filepath.Base(path), err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func scalar(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func appliedVersion(t *testing.T, path string) int64 {
	t.Helper()
	return scalar(t, openFrozen(t, path), `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`)
}

func projectIDs(t *testing.T, path string) []string {
	t.Helper()
	rows, err := openFrozen(t, path).Query(`SELECT id FROM projects ORDER BY id`)
	if err != nil {
		t.Fatalf("read projects: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan project: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read projects: %v", err)
	}
	return ids
}

func backupNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read backups: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func requireNoBackups(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "backups")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("backups dir exists (stat err %v), want none: %v", err, backupNames(t, dir))
	}
}

func plentyOfSpace(string) (uint64, error) { return 1 << 60, nil }

func plainCopy(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func TestOpenSnapshotsBeforeApplyingPendingMigrations(t *testing.T) {
	latest := latestMigration(t)
	from := latest - 2
	dir := t.TempDir()
	seed := oldDatabase(t, dir, from, "before-upgrade")
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	snapshot := snapshotFileFor(dir, from, latest)
	if got := appliedVersion(t, snapshot); got != from {
		t.Errorf("snapshot is at migration %d, want the pre-migration %d", got, from)
	}
	if got := projectIDs(t, snapshot); !slices.Equal(got, []string{"before-upgrade"}) {
		t.Errorf("snapshot projects = %v, want the pre-migration rows", got)
	}
	if got := appliedVersion(t, filepath.Join(dir, "ao.db")); got != latest {
		t.Errorf("database is at migration %d after Open, want %d", got, latest)
	}
	if got := backupNames(t, dir); !slices.Equal(got, []string{filepath.Base(snapshot)}) {
		t.Errorf("backups = %v, want only %s", got, filepath.Base(snapshot))
	}
}

func TestOpenDoesNotSnapshotWhenNothingIsPending(t *testing.T) {
	t.Run("new install", func(t *testing.T) {
		dir := t.TempDir()
		store, err := Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		requireNoBackups(t, dir)
	})
	t.Run("current database", func(t *testing.T) {
		dir := t.TempDir()
		seed := oldDatabase(t, dir, latestMigration(t), "p")
		if err := seed.Close(); err != nil {
			t.Fatalf("close seed: %v", err)
		}
		for range 2 { // and again on the next start
			store, err := Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}
			requireNoBackups(t, dir)
		}
	})
}

// A repair is not a migration: reconcileSchema re-adds a column that a burned
// ledger skipped, on every start, and that alone does not earn a snapshot.
func TestOpenDoesNotSnapshotForARepairAlone(t *testing.T) {
	dir := t.TempDir()
	seed := openMigratedDatabaseCopyAt(t, dir, latestMigration(t), pragmas)
	mustExec(t, seed, `ALTER TABLE conversations DROP COLUMN opencode_mode`)
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	live := openFrozen(t, filepath.Join(dir, "ao.db"))
	if scalar(t, live, `SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name = 'opencode_mode'`) != 1 {
		t.Fatal("test setup: the schema repair did not run")
	}
	requireNoBackups(t, dir)
}

// Open snapshots before migrate() runs any renumbering repair, so the snapshot
// holds the ledger exactly as the previous build left it. A preview build
// recorded the Cues migration as 0162, a number main later gave to something
// else; repairRenumberedCueMigrationHistory moves it to 0168.
func TestOpenSnapshotsBeforeRenumberRepairs(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dir, 161, pragmas)
	seedPreviewCues(t, db, 162)
	if err := db.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	ledger := `SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`
	snapshot := openFrozen(t, snapshotFileFor(dir, 162, latest))
	if scalar(t, snapshot, ledger, 162) != 1 || scalar(t, snapshot, ledger, 168) != 0 {
		t.Error("snapshot ledger was already repaired; it should hold the legacy 0162 and no 0168")
	}
	live := openFrozen(t, filepath.Join(dir, "ao.db"))
	if scalar(t, live, ledger, 168) == 0 {
		t.Error("live ledger has no 0168; the renumber repair did not run")
	}
}

func TestOpenKeepsStartingWhenTheSnapshotCannotBeWritten(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	seed := oldDatabase(t, dir, latest-1, "p")
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}
	// A file where the backups directory belongs: MkdirAll cannot succeed.
	if err := os.WriteFile(filepath.Join(dir, "backups"), []byte("in the way"), 0o600); err != nil {
		t.Fatalf("block backups dir: %v", err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open must not fail because a recovery copy could not be made: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if got := appliedVersion(t, filepath.Join(dir, "ao.db")); got != latest {
		t.Errorf("database is at migration %d, want %d: migrations must still run", got, latest)
	}
}

// The checkpoint is what makes the clone of ao.db alone complete: rows that
// were committed but still sit in the WAL must be in the snapshot.
func TestSnapshotHoldsRowsCommittedToTheWAL(t *testing.T) {
	dir := t.TempDir()
	db := oldDatabase(t, dir, latestMigration(t)-1, "only-in-the-wal")
	if info, err := os.Stat(filepath.Join(dir, "ao.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("test setup: want frames in ao.db-wal, got %v (%v)", info, err)
	}

	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("takeSnapshot: %v", err)
	}

	snapshot := snapshotFileFor(dir, latestMigration(t)-1, latestMigration(t))
	if got := projectIDs(t, snapshot); !slices.Equal(got, []string{"only-in-the-wal"}) {
		t.Fatalf("snapshot projects = %v, want the row that was only in the WAL", got)
	}
}

func TestSnapshotSkipsWhenTheCheckpointCannotComplete(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	db := oldDatabase(t, dir, latest-1, "p1")

	// A second connection pins a read snapshot, so the writer cannot fold the
	// frames written after it back into ao.db and reset the WAL.
	reader, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	tx, err := reader.Begin()
	if err != nil {
		t.Fatalf("begin read transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var pinned int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&pinned); err != nil {
		t.Fatalf("pin read snapshot: %v", err)
	}
	addProject(t, db, "p2")
	mustExec(t, db, `PRAGMA busy_timeout = 0`)

	err = takeSnapshot(context.Background(), dir, db, plainCopy, plentyOfSpace)
	if err == nil {
		t.Fatal("takeSnapshot succeeded although the WAL could not be checkpointed; the copy would be stale")
	}
	if got := backupNames(t, dir); len(got) != 0 {
		t.Errorf("backups = %v, want none", got)
	}
}

func TestSnapshotKeepsOnlyTheNewest(t *testing.T) {
	latest := latestMigration(t)
	from := latest - 1
	dir := t.TempDir()
	db := oldDatabase(t, dir, from, "p")
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	for _, name := range []string{
		"ao-pre-0001-to-0002.db",
		// left behind by a tool that opened the old snapshot
		"ao-pre-0001-to-0002.db-wal",
		// interrupted copies: one of another upgrade, one of this very upgrade
		"ao-pre-0003-to-0004.db.tmp",
		fmt.Sprintf("ao-pre-%04d-to-%04d.db.tmp", from, latest),
		// not ours: a person's own files stay, even when named like a snapshot
		"notes.txt",
		"ao-pre-keep-me.db",
	} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("old"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("takeSnapshot: %v", err)
	}

	want := []string{filepath.Base(snapshotFileFor(dir, from, latest)), "ao-pre-keep-me.db", "notes.txt"}
	if got := backupNames(t, dir); !slices.Equal(got, want) {
		t.Errorf("backups = %v, want %v", got, want)
	}
}

func TestSnapshotRefreshesTheSnapshotOfTheSameUpgrade(t *testing.T) {
	latest := latestMigration(t)
	from := latest - 1
	dir := t.TempDir()
	db := oldDatabase(t, dir, from, "a")
	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("first takeSnapshot: %v", err)
	}

	// The database went back to this version (a restored backup) and moved on.
	addProject(t, db, "b")
	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("second takeSnapshot: %v", err)
	}

	if got := projectIDs(t, snapshotFileFor(dir, from, latest)); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("snapshot projects = %v, want the newer contents", got)
	}
}

// A migration that fails partway leaves the database ahead of where it started.
// The restart must not replace the untouched pre-upgrade snapshot with one of
// that half-migrated database.
func TestSnapshotKeepsTheOriginalWhenTheSameUpgradeIsRetried(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	db := oldDatabase(t, dir, latest-2, "pristine")
	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("first takeSnapshot: %v", err)
	}

	upTo(t, db, latest-1) // the first migration lands; the last one fails
	addProject(t, db, "after-the-first-migration")
	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("retry takeSnapshot: %v", err)
	}

	original := filepath.Base(snapshotFileFor(dir, latest-2, latest))
	if got := backupNames(t, dir); !slices.Equal(got, []string{original}) {
		t.Fatalf("backups = %v, want only the original %s", got, original)
	}
	if got := projectIDs(t, snapshotFileFor(dir, latest-2, latest)); !slices.Equal(got, []string{"pristine"}) {
		t.Errorf("original snapshot projects = %v, want only the pre-upgrade rows", got)
	}
}

// A hole below the head is pending too. Once the upgrade that took the earlier
// snapshot has finished, that snapshot is not "the original of a retried
// upgrade", so the hole gets a snapshot of its own.
func TestSnapshotIsRetakenForAHoleAfterTheUpgradeFinished(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	db := oldDatabase(t, dir, latest-1, "before-the-upgrade")
	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("first takeSnapshot: %v", err)
	}
	upTo(t, db, latest) // the upgrade finishes
	addProject(t, db, "after-the-upgrade")
	mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id = 100`)

	if err := takeSnapshot(context.Background(), dir, db, cloneFile, plentyOfSpace); err != nil {
		t.Fatalf("second takeSnapshot: %v", err)
	}

	fresh := snapshotFileFor(dir, latest, latest)
	if got := backupNames(t, dir); !slices.Equal(got, []string{filepath.Base(fresh)}) {
		t.Fatalf("backups = %v, want only %s", got, filepath.Base(fresh))
	}
	if got := projectIDs(t, fresh); !slices.Equal(got, []string{"after-the-upgrade", "before-the-upgrade"}) {
		t.Errorf("snapshot projects = %v, want the current contents", got)
	}
}

// A full copy reads ao.db for seconds. If another process checkpointed into the
// file meanwhile, the copy would be torn and, being the newest, would replace a
// good older snapshot. takeSnapshot therefore holds a read transaction while it
// copies: a second connection (standing in for another process) can still write
// to the WAL but cannot checkpoint it into ao.db until the reader is gone.
func TestSnapshotCopyKeepsAReaderSoNoOtherCheckpointCanTearIt(t *testing.T) {
	latest := latestMigration(t)
	dir := t.TempDir()
	db := oldDatabase(t, dir, latest-1, "before")
	dbFile := filepath.Join(dir, "ao.db")
	other, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	other.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = other.Close() })
	mustExec(t, other, `PRAGMA busy_timeout = 0`)
	digest := func() [32]byte {
		data, err := os.ReadFile(dbFile)
		if err != nil {
			t.Fatalf("read ao.db: %v", err)
		}
		return sha256.Sum256(data)
	}
	// blocked reports whether the other connection's TRUNCATE checkpoint could not finish.
	blocked := func() bool {
		var busy, frames, moved int
		if err := other.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &moved); err != nil {
			return true
		}
		return busy != 0
	}

	// The space probe runs inside the section where the copy is made, so it is
	// where the "other process" acts.
	var checkpointBlocked, fileUntouched bool
	probe := func(string) (uint64, error) {
		before := digest()
		addProject(t, other, "written-during-the-copy")
		checkpointBlocked = blocked()
		fileUntouched = digest() == before
		return 1 << 60, nil
	}
	noClone := func(string, string) error { return errors.ErrUnsupported }
	if err := takeSnapshot(context.Background(), dir, db, noClone, probe); err != nil {
		t.Fatalf("takeSnapshot: %v", err)
	}

	if !checkpointBlocked || !fileUntouched {
		t.Errorf("while the snapshot was copied: other checkpoint blocked = %v, ao.db unchanged = %v; want both true", checkpointBlocked, fileUntouched)
	}
	if got := projectIDs(t, snapshotFileFor(dir, latest-1, latest)); !slices.Equal(got, []string{"before"}) {
		t.Errorf("snapshot projects = %v, want the state before the concurrent write", got)
	}
	// The reader is gone afterwards: the same checkpoint now goes through (so the
	// block above was the reader's doing), and the writer still migrates.
	before := digest()
	if blocked() || digest() == before {
		t.Error("the other connection's checkpoint is still blocked after takeSnapshot returned")
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate after the snapshot: %v", err)
	}
	if got := scalar(t, db, `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`); got != latest {
		t.Errorf("database is at migration %d after migrate, want %d", got, latest)
	}
}

func TestSnapshotPrefersTheCloneOverTheSpaceCheck(t *testing.T) {
	dir := t.TempDir()
	db := oldDatabase(t, dir, latestMigration(t)-1, "p")
	cloned := false
	clone := func(src, dst string) error {
		cloned = true
		return plainCopy(src, dst)
	}
	probe := func(string) (uint64, error) {
		t.Error("free space was probed although the clone succeeded")
		return 0, nil
	}

	if err := takeSnapshot(context.Background(), dir, db, clone, probe); err != nil {
		t.Fatalf("takeSnapshot: %v", err)
	}
	if !cloned {
		t.Error("clone was not attempted")
	}
}

func TestSnapshotFullCopyNeedsDatabaseSizePlusTenPercentPlusHeadroom(t *testing.T) {
	latest := latestMigration(t)
	from := latest - 1
	dir := t.TempDir()
	db := oldDatabase(t, dir, from, "p")
	mustExec(t, db, `PRAGMA wal_checkpoint(TRUNCATE)`)
	info, err := os.Stat(filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatalf("stat ao.db: %v", err)
	}
	size := uint64(info.Size())
	need := size + size/10 + snapshotHeadroom
	noClone := func(string, string) error { return errors.ErrUnsupported }
	free := func(n uint64) func(string) (uint64, error) {
		return func(string) (uint64, error) { return n, nil }
	}
	// An older snapshot must survive a snapshot that cannot be made.
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	older := filepath.Join(backups, "ao-pre-0001-to-0002.db")
	if err := os.WriteFile(older, []byte("older"), 0o600); err != nil {
		t.Fatalf("write older snapshot: %v", err)
	}

	err = takeSnapshot(context.Background(), dir, db, noClone, free(need-1))
	if !errors.Is(err, errSnapshotNoSpace) {
		t.Fatalf("one byte short: err = %v, want errSnapshotNoSpace", err)
	}
	if got := backupNames(t, dir); !slices.Equal(got, []string{"ao-pre-0001-to-0002.db"}) {
		t.Errorf("after a refused copy backups = %v, want only the older snapshot", got)
	}

	if err := takeSnapshot(context.Background(), dir, db, noClone, free(need)); err != nil {
		t.Fatalf("exactly enough space: %v", err)
	}
	snapshot := snapshotFileFor(dir, from, latest)
	if got := projectIDs(t, snapshot); !slices.Equal(got, []string{"p"}) {
		t.Errorf("full-copy snapshot projects = %v, want [p]", got)
	}
	if got := backupNames(t, dir); !slices.Equal(got, []string{filepath.Base(snapshot)}) {
		t.Errorf("backups = %v, want only the new snapshot", got)
	}

	if err := takeSnapshot(context.Background(), dir, db, noClone, func(string) (uint64, error) {
		return 0, errors.New("statfs failed")
	}); err == nil {
		t.Error("unknown free space: takeSnapshot copied anyway")
	}
}

func TestCloneFileCopiesContent(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	want := bytes.Repeat([]byte("ao"), 1<<16)
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := cloneFile(src, dst); err != nil {
		// A volume without clones (HFS+, ext4, tmpfs) is not a failure. On macOS
		// anything else is: every APFS volume can clone.
		if errors.Is(err, errors.ErrUnsupported) || runtime.GOOS != "darwin" {
			t.Skipf("no copy-on-write clone here: %v", err)
		}
		t.Fatalf("clonefile: %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("clone content differs from the source (read err %v)", err)
	}
	if err := os.WriteFile(dst, []byte("changed"), 0o600); err != nil {
		t.Fatalf("write clone: %v", err)
	}
	if got, err := os.ReadFile(src); err != nil || !bytes.Equal(got, want) {
		t.Errorf("writing the clone changed the source (read err %v)", err)
	}
	if err := cloneFile(src, dst); err == nil {
		t.Error("clone overwrote an existing file")
	}
}

func TestPendingMigrations(t *testing.T) {
	latest := latestMigration(t)
	for _, tc := range []struct {
		name    string
		db      func(t *testing.T) *sql.DB
		from    int64
		pending bool
	}{
		{"no ledger", func(t *testing.T) *sql.DB {
			db, err := sql.Open("sqlite", databaseURI(t.TempDir())+pragmas)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			return db
		}, 0, false},
		{"ledger without a single applied migration", func(t *testing.T) *sql.DB {
			db := openMigratedDatabaseCopy(t, latest-1)
			mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id <> 0`)
			return db
		}, 0, false},
		{"behind", func(t *testing.T) *sql.DB { return openMigratedDatabaseCopy(t, latest-2) }, latest - 2, true},
		{"current", func(t *testing.T) *sql.DB { return openMigratedDatabaseCopy(t, latest) }, latest, false},
		{"hole below the head", func(t *testing.T) *sql.DB {
			db := openMigratedDatabaseCopy(t, latest)
			mustExec(t, db, `DELETE FROM goose_db_version WHERE version_id = 100`)
			return db
		}, latest, true},
		{"newer than this build", func(t *testing.T) *sql.DB {
			db := openMigratedDatabaseCopy(t, latest)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, latest+5)
			return db
		}, latest + 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := tc.db(t)
			tablesBefore := scalar(t, db, `SELECT COUNT(*) FROM sqlite_master`)
			rowsBefore := int64(-1)
			if scalar(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'goose_db_version'`) == 1 {
				rowsBefore = scalar(t, db, `SELECT COUNT(*) FROM goose_db_version`)
			}

			from, to, pending, err := pendingMigrations(context.Background(), db)
			if err != nil {
				t.Fatalf("pendingMigrations: %v", err)
			}
			if from != tc.from || pending != tc.pending {
				t.Errorf("from, pending = %d, %v; want %d, %v", from, pending, tc.from, tc.pending)
			}
			if pending && to != latest {
				t.Errorf("to = %d, want the embedded head %d", to, latest)
			}

			// Finding out must not change anything: no ledger created, no row added.
			if got := scalar(t, db, `SELECT COUNT(*) FROM sqlite_master`); got != tablesBefore {
				t.Errorf("schema object count %d -> %d: pendingMigrations created something", tablesBefore, got)
			}
			if rowsBefore >= 0 {
				if got := scalar(t, db, `SELECT COUNT(*) FROM goose_db_version`); got != rowsBefore {
					t.Errorf("ledger rows %d -> %d: pendingMigrations wrote to the ledger", rowsBefore, got)
				}
			}
		})
	}
}
