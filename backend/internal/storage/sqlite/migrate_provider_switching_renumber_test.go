package sqlite

import (
	"os"
	"strings"
	"testing"
)

func TestMigrateMovesProviderSwitchingFromItsBranchNumbers(t *testing.T) {
	for _, old := range []int64{201, 216} {
		db := openMigratedDatabaseCopy(t, providerSwitchingVersion)
		// As a database that ran a preview build, where the migration had this number.
		if _, err := db.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = ?`, old, providerSwitchingVersion); err != nil {
			t.Fatalf("seed branch numbering: %v", err)
		}
		if err := migrate(db); err != nil {
			t.Fatalf("migrate a database that ran it as %d: %v", old, err)
		}
		var atOld, atNew int
		if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, old).Scan(&atOld); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion).Scan(&atNew); err != nil {
			t.Fatal(err)
		}
		if atOld != 0 || atNew != 1 {
			t.Fatalf("versions %d=%d new=%d, want the migration recorded once at its version", old, atOld, atNew)
		}
		if err := migrate(db); err != nil {
			t.Fatalf("second migrate: %v", err)
		}
	}
}

func TestMigrateOpensAPreviewDatabaseThatRanProviderSwitchingAs0196(t *testing.T) {
	// A preview build ran this migration as 0196, before main's 0196-0200 existed.
	db := openMigratedDatabaseCopy(t, 195)
	sqlText, err := os.ReadFile("migrations/20261020131539535_provider_switching.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(strings.TrimPrefix(string(sqlText), "-- +goose Up"), "-- +goose Down")
	if _, err := db.Exec(up); err != nil {
		t.Fatalf("apply the branch migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (196, 1)`); err != nil {
		t.Fatalf("record it as 196: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate the preview database: %v", err)
	}
	var mainTable, ours int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_artifact_versions'`).Scan(&mainTable); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, providerSwitchingVersion).Scan(&ours); err != nil {
		t.Fatal(err)
	}
	if mainTable != 1 || ours != 1 {
		t.Fatalf("main's 0196 table=%d, provider switching recorded=%d; want both", mainTable, ours)
	}
}
