package sqlite

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestMigrateUsageLimitAfterMainAndForeignVersion(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "main 0206"
		if foreign {
			name = "main with unrelated 0201"
		}
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 206)
			if foreign {
				mustExec(t, db, `CREATE TABLE foreign_201 (value TEXT);
INSERT INTO foreign_201 VALUES ('preserved');
INSERT INTO goose_db_version (version_id, is_applied) VALUES (201, 1);`)
			}
			for range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate: %v", err)
				}
				assertAppliedMigrations(t, db, 206, 213, 214, 215, usageLimitMigrationVersion, usageLimitResetMigrationVersion)
				assertUsageLimitMigrationApplied(t, db)
				var receipts int
				if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 201 AND is_applied = 1`).Scan(&receipts); err != nil {
					t.Fatal(err)
				}
				want := 0
				if foreign {
					want = 1
					var value string
					if err := db.QueryRow(`SELECT value FROM foreign_201`).Scan(&value); err != nil || value != "preserved" {
						t.Fatalf("foreign data = %q, %v", value, err)
					}
				}
				if receipts != want {
					t.Fatalf("foreign version 201 receipts = %d, want %d", receipts, want)
				}
			}
		})
	}
}

func TestMigrateRepairsLegacyUsageLimitHistory(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 200)
	applyLegacyCodexProfileMigrations(t, db, []legacyCodexProfileMigration{{
		version: 201, canonicalPath: "migrations/20261019220421171_usage_limit_auto_resume.sql", legacyName: "usage_limit_auto_resume.sql",
	}})
	reset := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `
INSERT INTO projects (id, path, registered_at) VALUES ('quota', '/quota', CURRENT_TIMESTAMP);
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES ('quota-1', 'quota', 1, 'codex', 'chat', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at)
VALUES ('quota-conversation', 'session', 'quota', 'quota-1', 'quota-1', 'quota-root', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO conversation_branches (id, conversation_id, session_id, provider_conversation_id, created_at)
VALUES ('quota-root', 'quota-conversation', 'quota-1', 'native-thread', CURRENT_TIMESTAMP);
INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, provider_turn_id, state, error_message,
    requested_at, completed_at, branch_id, usage_limit_resets_at, usage_limit_auto_resume, usage_limit_resume_error)
VALUES ('quota-turn', 'quota-conversation', 'quota-1', 'native-turn', 'failed', 'Usage limit reached',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'quota-root', ?, 1, 'saved diagnostic');
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at, branch_id)
VALUES ('quota-message', 'quota-conversation', 'quota-turn', 1, 'user', 'human', 'finish this task',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'quota-root');`, reset)
	var receiptID int64
	if err := db.QueryRow(`SELECT id FROM goose_db_version WHERE version_id = 201 AND is_applied = 1`).Scan(&receiptID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate legacy usage-limit schema: %v", err)
		}
		assertAppliedMigrations(t, db, 201, 206, 213, 214, 215, usageLimitMigrationVersion, usageLimitResetMigrationVersion)
		assertUsageLimitMigrationApplied(t, db)
		var oldReceipt int64
		if err := db.QueryRow(`SELECT id FROM goose_db_version WHERE version_id = 201 AND is_applied = 1`).Scan(&oldReceipt); err != nil || oldReceipt != receiptID {
			t.Fatalf("legacy receipt changed: %d, %v", oldReceipt, err)
		}
		var gotReset time.Time
		var autoResume int
		var state, failure, resumeError, providerTurn, prompt string
		if err := db.QueryRow(`SELECT t.state, t.error_message, t.usage_limit_resets_at, t.usage_limit_auto_resume,
    t.usage_limit_resume_error, t.provider_turn_id, m.text FROM conversation_turns t
JOIN conversation_messages m ON m.turn_id = t.id WHERE t.id = 'quota-turn'`).Scan(
			&state, &failure, &gotReset, &autoResume, &resumeError, &providerTurn, &prompt,
		); err != nil {
			t.Fatal(err)
		}
		if state != "failed" || failure != "Usage limit reached" || !gotReset.Equal(reset) || autoResume != 1 ||
			resumeError != "saved diagnostic" || providerTurn != "native-turn" || prompt != "finish this task" {
			t.Fatalf("legacy retry data changed: state=%q failure=%q reset=%v auto=%d diagnostic=%q provider=%q prompt=%q",
				state, failure, gotReset, autoResume, resumeError, providerTurn, prompt)
		}
	}
}

func TestUsageLimitRenumberRefusesIncompleteSchema(t *testing.T) {
	for _, tc := range []struct{ name, remove string }{
		{"missing index", `DROP INDEX conversation_turns_auto_resume`},
		{"missing trigger", `DROP TRIGGER conversation_turns_auto_resume_cdc_update`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 200)
			applyLegacyCodexProfileMigrations(t, db, []legacyCodexProfileMigration{{
				version: 201, canonicalPath: "migrations/20261019220421171_usage_limit_auto_resume.sql", legacyName: "usage_limit_auto_resume.sql",
			}})
			mustExec(t, db, tc.remove)
			if err := repairRenumberedUsageLimitMigrationHistory(db); err != nil {
				t.Fatal(err)
			}
			assertAppliedMigrations(t, db, 201)
			var receipts int
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, usageLimitMigrationVersion).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("incomplete schema marked as applied: receipts=%d err=%v", receipts, err)
			}
		})
	}
}

func TestMigrateUsageLimitResetCDCFromCanonical(t *testing.T) {
	db := openMigratedDatabaseCopy(t, usageLimitMigrationVersion)
	var trigger string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'conversation_turns_auto_resume_cdc_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(trigger, "usage_limit_resets_at") {
		t.Fatal("canonical fixture already has reset CDC")
	}
	for range 2 {
		if err := migrate(db); err != nil {
			t.Fatal(err)
		}
		assertAppliedMigrations(t, db, usageLimitMigrationVersion, usageLimitResetMigrationVersion)
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'conversation_turns_auto_resume_cdc_update'`).Scan(&trigger); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(trigger, "OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at") {
			t.Fatalf("reset migration did not upgrade reset CDC: %s", trigger)
		}
	}
}

func assertUsageLimitMigrationApplied(t *testing.T, db *sql.DB) {
	t.Helper()
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversation_turns')
WHERE name IN ('usage_limit_resets_at', 'usage_limit_auto_resume', 'usage_limit_resume_error')`).Scan(&columns); err != nil || columns != 3 {
		t.Fatalf("usage-limit column count = %d, %v", columns, err)
	}
	var objects, receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('conversation_turns_auto_resume', 'conversation_turns_auto_resume_cdc_update')`).Scan(&objects); err != nil || objects != 2 {
		t.Fatalf("usage-limit index/trigger count = %d, %v", objects, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, usageLimitMigrationVersion).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("canonical usage-limit receipts = %d, %v", receipts, err)
	}
}
