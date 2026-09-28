package sqlite

import (
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

func TestMigrateRepairsSideChatMigrationAtOldVersion(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 155)
	contents, err := migrationsFS.ReadFile("migrations/0163_conversation_side_chats.sql")
	if err != nil {
		t.Fatal(err)
	}
	gooseMu.Lock()
	goose.SetBaseFS(fstest.MapFS{
		"migrations/0156_conversation_side_chats.sql": &fstest.MapFile{Data: contents},
	})
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		gooseMu.Unlock()
		t.Fatal(err)
	}
	err = goose.Up(db, "migrations", goose.WithAllowMissing())
	goose.SetBaseFS(migrationsFS)
	gooseMu.Unlock()
	if err != nil {
		t.Fatalf("apply old side-chat migration: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate old side-chat database: %v", err)
	}
	for _, check := range []struct{ table, column string }{
		{"conversation_branches", "purpose"},
		{"conversation_branches", "label"},
		{"sessions", "provision_state"},
		{"sessions", "provision_error"},
	} {
		var present int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, check.table, check.column).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present != 1 {
			t.Errorf("missing %s.%s", check.table, check.column)
		}
	}
	for _, version := range []int{156, 163} {
		var applied int
		if err := db.QueryRow(`SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1`, version).Scan(&applied); err != nil {
			t.Fatal(err)
		}
		if applied != 1 {
			t.Errorf("migration %d was not applied", version)
		}
	}
}
