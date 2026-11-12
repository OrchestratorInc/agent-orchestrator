package sqlite

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

// priorWorktreeRootMigration is the version immediately before the first of the
// two worktree-root migrations, so DownTo rolls both of them back.
const priorWorktreeRootMigration int64 = 20261111070458370

// TestWorktreeRootMigrationsDownUpRoundTrip covers the two worktree-root
// migrations' Down halves: sqlite supports the column drop, and an existing
// session row survives the round trip. A migration that cannot be rolled back
// blocks every downgrade path, and one that loses rows would lose the sessions
// the snapshot exists to protect.
func TestWorktreeRootMigrationsDownUpRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	// Open (and migrate) through the store, then drive goose directly on the file
	// so the Down halves can be exercised.
	store, err := Open(dataDir)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	db, err := sql.Open("sqlite", databaseURI(dataDir))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at, kind) VALUES ('rt', '/tmp/rt', '2026-01-01T00:00:00Z', 'single_repo')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at, worktree_root)
		VALUES ('rt-1', 'rt', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '/custom/root')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session_worktrees (session_id, repo_name, branch, base_sha, worktree_path, worktree_root)
		VALUES ('rt-1', '__root__', 'ao/rt-1', 'abc123', '/custom/root/rt/rt-1', '/custom/root')`); err != nil {
		t.Fatalf("seed worktree row: %v", err)
	}

	gooseMu.Lock()
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatalf("set dialect: %v", err)
	}
	// Roll BOTH worktree-root migrations back (DownTo is exclusive of the target),
	// then forward again.
	if err := goose.DownTo(db, "migrations", priorWorktreeRootMigration); err != nil {
		gooseMu.Unlock()
		t.Fatalf("down to worktree-root migrations: %v", err)
	}
	for _, probe := range []struct{ table, column string }{
		{"sessions", "worktree_root"},
		{"session_worktrees", "worktree_root"},
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, probe.table, probe.column).Scan(&count); err != nil {
			gooseMu.Unlock()
			t.Fatalf("probe %s.%s: %v", probe.table, probe.column, err)
		}
		if count != 0 {
			gooseMu.Unlock()
			t.Fatalf("%s.%s still present after Down", probe.table, probe.column)
		}
	}
	var sessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'rt-1'`).Scan(&sessions); err != nil {
		gooseMu.Unlock()
		t.Fatalf("count sessions after Down: %v", err)
	}
	if sessions != 1 {
		gooseMu.Unlock()
		t.Fatalf("sessions after Down = %d, want the row preserved", sessions)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		gooseMu.Unlock()
		t.Fatalf("up after down: %v", err)
	}
	gooseMu.Unlock()

	// After re-applying, the columns exist and default to empty for existing rows:
	// the snapshot is evidence, so a rollback must not invent one.
	var root string
	if err := db.QueryRow(`SELECT worktree_root FROM sessions WHERE id = 'rt-1'`).Scan(&root); err != nil {
		t.Fatalf("read session root after Up: %v", err)
	}
	if root != "" {
		t.Fatalf("session root after re-migration = %q, want empty (the column was dropped)", root)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_worktrees WHERE session_id = 'rt-1'`).Scan(&rowCount); err != nil {
		t.Fatalf("count worktree rows after Up: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("worktree rows after Up = %d, want the row preserved", rowCount)
	}
}
