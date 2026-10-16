package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	backupsDirName = "backups"

	// snapshotHeadroom is the free space a full-copy snapshot must leave behind
	// on top of the copy itself, so the migrations that follow still have room
	// (a table rebuild needs about the table's size again).
	snapshotHeadroom = 256 << 20
)

// snapshotFile matches the files this code owns in backups: a snapshot,
// ao-pre-<from>-to-<to>.db, its interrupted ".tmp", and the "-wal" or "-shm" a
// tool leaves beside a snapshot it opened. Any other name belongs to the user.
// Group 3 is the suffix; it is empty for a finished snapshot.
var snapshotFile = regexp.MustCompile(`^ao-pre-(\d+)-to-(\d+)\.db(\.tmp|-wal|-shm)?$`)

// errSnapshotNoSpace means the volume cannot clone ao.db and cannot hold a full
// copy either.
var errSnapshotNoSpace = errors.New("not enough free space for a full copy of ao.db")

// snapshotBeforeMigrate keeps a recovery copy of ao.db in <dataDir>/backups
// before migrate() rewrites the schema in place. It is best effort: a
// migration that runs without a backup is no worse than before, so a failure
// is logged and never stops Open.
func snapshotBeforeMigrate(ctx context.Context, dataDir string, db *sql.DB) {
	if err := takeSnapshot(ctx, dataDir, db, cloneFile, freeBytes); err != nil {
		slog.Default().Warn("sqlite: no pre-migration snapshot; migrating without one", "error", err)
	}
}

// takeSnapshot writes backups/ao-pre-<from>-to-<to>.db when the database has
// migrations pending, and keeps only that newest snapshot. clone makes a
// copy-on-write clone of a file; freeSpace reports the bytes free on a
// directory's volume. Both are parameters so tests can stand in for volumes.
//
// The snapshot is one self-contained file: the WAL is checkpointed into ao.db
// (TRUNCATE) on the writer first, then ao.db alone is cloned. Cloning ao.db
// and ao.db-wal instead would take two clones one after the other and leave
// two files that only work together. The checkpoint is cheap here because
// nothing else has the database open yet. If it cannot finish (another
// connection is still using the database), ao.db is behind the WAL and the
// snapshot is skipped rather than made stale.
//
// A restart after an upgrade that failed partway keeps the original snapshot
// of that upgrade (see keepsEarlierSnapshot) instead of replacing it.
func takeSnapshot(
	ctx context.Context,
	dataDir string,
	db *sql.DB,
	clone func(src, dst string) error,
	freeSpace func(dir string) (uint64, error),
) error {
	dbPath := filepath.Join(dataDir, "ao.db")
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return nil // a new install has nothing to protect
	} else if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	from, to, pending, err := pendingMigrations(ctx, db)
	if err != nil {
		return fmt.Errorf("find pending migrations: %w", err)
	}
	if !pending {
		return nil
	}
	backups := filepath.Join(dataDir, backupsDirName)
	if keepsEarlierSnapshot(backups, from, to) {
		slog.Default().Info("sqlite: keeping the snapshot from an earlier attempt at this upgrade", "from", from, "to", to)
		return nil
	}
	if err := os.MkdirAll(backups, 0o750); err != nil {
		return fmt.Errorf("create backups dir: %w", err)
	}

	start := time.Now()
	var busy, walFrames, checkpointed int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &walFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint WAL: %w", err)
	}
	if busy != 0 {
		return errors.New("checkpoint WAL: another connection is using the database, so ao.db does not yet hold every committed write")
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}

	name := fmt.Sprintf("ao-pre-%04d-to-%04d.db", from, to)
	final := filepath.Join(backups, name)
	tmp := final + ".tmp"
	method, err := writeSnapshot(ctx, db, dbPath, tmp, info.Size(), clone, freeSpace)
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	pruneSnapshots(backups, name)
	slog.Default().Info("sqlite: pre-migration snapshot saved",
		"file", final, "from", from, "to", to, "method", method, "bytes", info.Size(), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// writeSnapshot puts a copy of src at dst: a copy-on-write clone when the volume
// can make one, else a full copy if the volume has room for the size-byte file,
// its 10% slack, and snapshotHeadroom. It reports which it made.
//
// A clone is atomic. A copy is not: it reads src for as long as it takes, and a
// checkpoint by another process into src meanwhile would tear it. So the copy
// runs with a read transaction open on db (see pinReader).
func writeSnapshot(
	ctx context.Context,
	db *sql.DB,
	src, dst string,
	size int64,
	clone func(src, dst string) error,
	freeSpace func(dir string) (uint64, error),
) (string, error) {
	_ = os.Remove(dst) // an interrupted earlier attempt
	cloneErr := clone(src, dst)
	if cloneErr == nil {
		return "clone", nil
	}
	_ = os.Remove(dst)
	n := uint64(size) //nolint:gosec // a regular file's size is never negative
	need := n + n/10 + snapshotHeadroom
	release, err := pinReader(ctx, db)
	if err != nil {
		return "", fmt.Errorf("no copy-on-write clone (%w) and could not pin ao.db for a copy: %w", cloneErr, err)
	}
	defer release()
	free, err := freeSpace(filepath.Dir(dst))
	if err != nil {
		return "", fmt.Errorf("no copy-on-write clone (%w) and free space unknown: %w", cloneErr, err)
	}
	if free < need {
		return "", fmt.Errorf("%w: need %d bytes, %d free (no copy-on-write clone: %w)", errSnapshotNoSpace, need, free, cloneErr)
	}
	slog.Default().Info("sqlite: copying ao.db before migrating", "bytes", size, "clone_error", cloneErr)
	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy ao.db: %w", err)
	}
	return "copy", nil
}

// pinReader opens a read transaction on the writer connection and returns the
// function that ends it. takeSnapshot has just emptied the WAL, so ao.db is the
// whole database. While a reader is open, no other process can checkpoint new
// WAL frames into ao.db (SQLite will not backfill past an active reader), so ao.db
// does not change under a copy. It only holds back checkpoints: other processes
// can still write. The data-dir lock makes a second writer impossible; this
// covers the period before it. A checkpoint that was already mid-backfill when
// the reader started could still overlap the copy, a window of the time between
// the TRUNCATE above and this call. The single writer connection is busy until
// release, so the caller must not use db meanwhile.
func pinReader(ctx context.Context, db *sql.DB) (release func(), err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var objects int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&objects); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return func() { _ = tx.Rollback() }, nil
}

// pendingMigrations reports the highest migration the database has applied
// (from), the highest this build embeds (to), and whether any embedded
// migration has no ledger row. That is what goose.Up(WithAllowMissing) applies:
// a version above the head, or a hole below it. It only reads.
//
// It runs before migrate()'s repairs, so it sees the ledger as the previous
// build left it. A renumbering repair fires only when the canonical version it
// records is not yet applied, and that version is pending here too, so a
// database such a repair would touch reports pending. A database whose only
// change would be a repair (reconcileSchema re-adding a missing column, which
// it checks on every start) has no migration pending and is not snapshotted.
// A repair that deletes a ledger row so goose replays it, on a ledger that
// otherwise looks complete, is not seen.
//
// A database with no ledger, or no applied migration, has nothing to preserve
// and is never pending.
func pendingMigrations(ctx context.Context, db *sql.DB) (from, to int64, pending bool, err error) {
	embedded, err := embeddedMigrationVersions()
	if err != nil {
		return 0, 0, false, err
	}
	to = slices.Max(embedded)

	var ledger int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&ledger); err != nil {
		return 0, 0, false, err
	}
	if ledger == 0 {
		return 0, to, false, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version_id, is_applied FROM goose_db_version`)
	if err != nil {
		return 0, 0, false, err
	}
	defer func() { _ = rows.Close() }()
	known := map[int64]bool{}
	for rows.Next() {
		var version int64
		var applied bool
		if err := rows.Scan(&version, &applied); err != nil {
			return 0, 0, false, err
		}
		known[version] = true
		if applied && version > from {
			from = version
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, false, err
	}
	if from == 0 {
		return 0, to, false, nil
	}
	for _, version := range embedded {
		if !known[version] {
			return from, to, true, nil
		}
	}
	return from, to, false, nil
}

// embeddedMigrationVersions lists the version of every embedded migration. It
// reads the file names, not goose's globals, so it needs no lock.
func embeddedMigrationVersions() ([]int64, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	versions := make([]int64, 0, len(entries))
	for _, entry := range entries {
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("embedded migration %s: %w", entry.Name(), err)
		}
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil, errors.New("no embedded migrations found")
	}
	return versions, nil
}

// keepsEarlierSnapshot reports whether backups already holds the snapshot of
// this same upgrade (same target) taken from an older version than the
// database is at now, while the database is still short of that target. That
// is what an upgrade that failed partway looks like after a restart: some
// migrations landed, so ao.db is no longer the state to roll back to, and a
// fresh snapshot would replace the untouched one. A snapshot from the same
// version is not kept: that is a database restored to an earlier state and
// upgraded again, and its old snapshot is stale. Neither is one when the
// database has reached the target and only a hole below it is pending: that
// upgrade finished, so its snapshot says nothing about this change.
func keepsEarlierSnapshot(backups string, from, to int64) bool {
	if from >= to {
		return false
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		m := snapshotFile.FindStringSubmatch(entry.Name())
		if len(m) == 0 || m[3] != "" {
			continue
		}
		earlierFrom, fromErr := strconv.ParseInt(m[1], 10, 64)
		earlierTo, toErr := strconv.ParseInt(m[2], 10, 64)
		if fromErr == nil && toErr == nil && earlierTo == to && earlierFrom < from {
			return true
		}
	}
	return false
}

// pruneSnapshots removes every file this code owns in backups (see
// snapshotFile) except keep: older snapshots, interrupted copies, and sidecars.
func pruneSnapshots(backups, keep string) {
	entries, err := os.ReadDir(backups)
	if err != nil {
		slog.Default().Warn("sqlite: list old snapshots", "error", err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || entry.IsDir() || !snapshotFile.MatchString(name) {
			continue
		}
		if err := os.Remove(filepath.Join(backups, name)); err != nil {
			slog.Default().Warn("sqlite: remove old snapshot", "file", name, "error", err)
		}
	}
}

// copyFile is the full-copy fallback. It is synced before the caller renames it
// into place, so a finished snapshot name never points at a short file.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
