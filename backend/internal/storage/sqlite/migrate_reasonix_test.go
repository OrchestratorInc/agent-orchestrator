package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestMigrateReasonixFreshSessionPersists(t *testing.T) {
	db := openMigratedTestDB(t)
	insertReasonixMigrationSession(t, db, "reasonix-1", 1, "reasonix")
	var harness, mode string
	if err := db.QueryRow(`SELECT harness, session_mode FROM sessions WHERE id = 'reasonix-1'`).Scan(&harness, &mode); err != nil {
		t.Fatal(err)
	}
	if harness != "reasonix" || mode != "tui" {
		t.Fatalf("persisted harness/mode = %q/%q, want reasonix/tui", harness, mode)
	}
}

func TestMigrateReasonixUpgradeAndDowngrade(t *testing.T) {
	for _, legacyQM := range []bool{false, true} {
		name := "current"
		if legacyQM {
			name = "legacy_qm"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 162)
			if legacyQM {
				if _, err := db.Exec(`PRAGMA writable_schema = ON;
UPDATE sqlite_master SET sql = replace(sql, '''unreal-agent'', ''fake''', '''unreal-agent'', ''qm'', ''fake''') WHERE type = 'table' AND name = 'sessions';
PRAGMA writable_schema = RESET;`); err != nil {
					t.Fatalf("seed legacy qm schema: %v", err)
				}
			}
			before := reasonixMigrationSchema(t, db)
			insertReasonixMigrationSession(t, db, "existing-1", 1, "unreal-agent")
			if legacyQM {
				insertReasonixMigrationSession(t, db, "legacy-2", 2, "qm")
			}
			upTo(t, db, 163)
			insertReasonixMigrationSession(t, db, "reasonix-3", 3, "reasonix")
			if _, err := db.Exec(`UPDATE sessions SET prompt = 'preserved after upgrade' WHERE id = 'existing-1'`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE sessions SET harness = 'codex' WHERE id = 'reasonix-3'`); err != nil {
				t.Fatal(err)
			}
			downTo(t, db, 162)
			if got := reasonixMigrationSchema(t, db); got != before {
				t.Fatalf("downgrade did not restore original sessions schema:\n%s", got)
			}
			if _, err := db.Exec(`INSERT INTO sessions (id, num, harness, activity_last_at, created_at, updated_at) VALUES ('rejected-4', 4, 'reasonix', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed") {
				t.Fatalf("Reasonix insert after downgrade err = %v, want CHECK constraint failure", err)
			}
			var prompt string
			if err := db.QueryRow(`SELECT prompt FROM sessions WHERE id = 'existing-1'`).Scan(&prompt); err != nil {
				t.Fatal(err)
			}
			if prompt != "preserved after upgrade" {
				t.Fatalf("existing session prompt = %q", prompt)
			}
			if legacyQM {
				insertReasonixMigrationSession(t, db, "legacy-5", 5, "qm")
			}
		})
	}
}

func insertReasonixMigrationSession(t *testing.T, db *sql.DB, id string, num int, harness string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO sessions (id, num, harness, session_mode, activity_last_at, created_at, updated_at) VALUES (?, ?, ?, 'tui', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, num, harness); err != nil {
		t.Fatalf("persist %s session: %v", harness, err)
	}
}

func reasonixMigrationSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}
