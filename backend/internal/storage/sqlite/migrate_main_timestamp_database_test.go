package sqlite

import (
	"database/sql"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// shadowed199FS returns the migration set with version 0199 replaced by a
// no-op — modeling a database whose version-199 slot was spent by another
// distribution. The wesleymatosdev fork's hermes-plus8 and hermes-v0.13.2 to
// v0.13.8 builds recorded 199 for 0199_allow_hermes_harness, after main had
// taken 199 for conversation_message_continuation. goose does not checksum
// applied migrations, so on such a database it sees 199 recorded and never
// runs main's file.
func shadowed199FS(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	legacyFS := fstest.MapFS{}
	for _, entry := range entries {
		name := entry.Name()
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		var version int64
		if _, err := fmt.Sscanf(name, "%d_", &version); err == nil && version == 199 {
			content = []byte("-- +goose Up\nSELECT 1;\n\n-- +goose Down\nSELECT 1;\n")
		}
		legacyFS["migrations/"+name] = &fstest.MapFile{Data: content}
	}
	return legacyFS
}

// openShadowed199Database builds the database the way those fork builds left
// it: every numeric and timestamp migration applied, except version 199 holds
// other content — so conversation_messages.continuation does not exist even
// though goose_db_version records 199 as applied.
func openShadowed199Database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", databaseURI(t.TempDir())+noForeignKeysDSN)
	if err != nil {
		t.Fatalf("open shadowed database: %v", err)
	}
	db.SetMaxOpenConns(1)

	gooseMu.Lock()
	goose.SetBaseFS(shadowed199FS(t))
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		gooseMu.Unlock()
		t.Fatalf("apply legacy (shadowed-199) migrations: %v", err)
	}
	gooseMu.Unlock()

	var applied199 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 199 AND is_applied = 1`).Scan(&applied199); err != nil {
		t.Fatalf("read version 199 history: %v", err)
	}
	if applied199 != 1 {
		t.Fatalf("setup: version 199 not recorded as applied (%d)", applied199)
	}
	var present int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`,
	).Scan(&present); err != nil {
		t.Fatalf("setup: inspect conversation_messages: %v", err)
	}
	if present != 0 {
		t.Fatalf("setup: continuation column already present; the shadowing scenario is not reproduced")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestMigrateUpgradesShadowed199Database covers the failure observed on
// 2026-10-29: after upgrading a database from those fork builds (or any
// database that recorded version 199 with other content), chat history
// returned HTTP 500 with "no such column: conversation_messages.continuation"
// for every chat session. goose_db_version already records 199 and goose does
// not checksum applied files, so Up never runs
// 0199_conversation_message_continuation. Fresh databases — and therefore the
// snapshot-based tests and CI — always apply main's 199 and cannot catch this.
//
// reconcileSchema's schemaRepairs entry for version 199 adds the missing
// column after goose runs, leaving the recorded version as it is.
func TestMigrateUpgradesShadowed199Database(t *testing.T) {
	db := openShadowed199Database(t)

	// The upgrade under test: open the legacy database with this build.
	if err := migrate(db); err != nil {
		t.Fatalf("migrate shadowed-199 database: %v", err)
	}

	// The contract the generated chat-history selects depend on (every
	// conversations.sql select names this column).
	var present int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`,
	).Scan(&present); err != nil {
		t.Fatalf("inspect conversation_messages: %v", err)
	}
	if present != 1 {
		t.Fatalf("0199_conversation_message_continuation was skipped on the upgraded database: " +
			"conversation_messages.continuation is missing, so chat-history reads fail with " +
			"'no such column: conversation_messages.continuation' (HTTP 500, observed in production). " +
			"Version 199 was already recorded with the legacy distribution's content, and goose " +
			"never re-runs a recorded version. Fresh databases hide the bug because they apply 199 " +
			"with its new meaning from the start.")
	}

	// The generated history select must run without a schema error.
	rows, err := db.Query(`
SELECT id, conversation_id, turn_id, sequence, revision, role, origin, text, streaming,
       provider_item_id, client_message_id, created_at, updated_at, delivery_content_json,
       continuation, sender_session_id, sender_project_id, sender_display_name
FROM conversation_messages`)
	if err != nil {
		t.Fatalf("chat-history select failed after upgrade: %v", err)
	}
	defer rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate chat-history select: %v", err)
	}
}

// TestMigrateUpgradesShadowed199DatabaseIdempotent pins the repair contract:
// repeated migrate passes over a healed database must keep version 199
// recorded exactly once with the schema stable.
func TestMigrateUpgradesShadowed199DatabaseIdempotent(t *testing.T) {
	db := openShadowed199Database(t)

	for i := 0; i < 2; i++ {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate pass %d: %v", i+1, err)
		}
	}
	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 199 AND is_applied = 1`,
	).Scan(&applied); err != nil {
		t.Fatalf("read version history: %v", err)
	}
	if applied != 1 {
		t.Fatalf("version 199 recorded %d times after two migrate passes, want exactly 1", applied)
	}
	var present int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name = 'continuation'`,
	).Scan(&present); err != nil {
		t.Fatalf("inspect conversation_messages: %v", err)
	}
	if present != 1 {
		t.Fatalf("continuation column missing after two migrate passes")
	}
}
