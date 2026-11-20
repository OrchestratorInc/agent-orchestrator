package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

// mastraCodeHarnessMigration is main's latest harness migration, the baseline
// CodeBuddy Code's migration follows.
const mastraCodeHarnessMigration int64 = 20261119090000000

func TestCodeBuddyMigrationRoundTripFromCurrentMain(t *testing.T) {
	db := openMigratedDatabaseCopy(t, mastraCodeHarnessMigration)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "'codebuddy'") {
		t.Fatal("pending CodeBuddy migration ran before the current main baseline")
	}
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('codebuddy-updown','/tmp/codebuddy-updown',CURRENT_TIMESTAMP)`)
	insert := `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at) VALUES (?,'codebuddy-updown',?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	upTo(t, db, codeBuddyHarnessMigration)
	mustExec(t, db, insert, "native", 1, "codebuddy")
	mustExec(t, db, insert, "main-mastra", 2, "mastra-code")
	if _, err := db.Exec(insert, "unknown", 3, "unknown-codebuddy"); err == nil {
		t.Fatal("unknown harness bypassed CHECK")
	}
	mustExec(t, db, `UPDATE sessions SET harness='' WHERE harness='codebuddy'`)
	downTo(t, db, mastraCodeHarnessMigration)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("down migration changed prior schema: %v", err)
	}
	if _, err := db.Exec(insert, "after-down", 4, "codebuddy"); err == nil {
		t.Fatal("downgrade still allows CodeBuddy")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %s, %v", integrity, err)
	}
}

// Down runs without a transaction. If it stops after the schema rewrite, the
// version stays applied with CodeBuddy Code already gone; a retried Down must
// finish instead of rejecting that restored constraint.
func TestCodeBuddyMigrationDownResumesAfterInterruptedRewrite(t *testing.T) {
	db := openMigratedDatabaseCopy(t, mastraCodeHarnessMigration)
	var before string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	upTo(t, db, codeBuddyHarnessMigration)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''codebuddy'', ', '') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	downTo(t, db, mastraCodeHarnessMigration)
	var after string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&after); err != nil || after != before {
		t.Fatalf("resumed down left schema %q, %v; want the prior schema", after, err)
	}
}

// A database that never ran an earlier harness migration: goose skips the
// burned number, and the repair must add CodeBuddy Code after every harness
// main added before it without losing any of them.
func TestReconcileCodeBuddyAfterBurnedMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 164)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (165, 1)`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, codeBuddyHarnessMigration)
	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned profile: %v", err)
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"'fx'", "'gemini'", "'mimo-code'", "'openhands'", "'command-code'", "'memcode'", "'tau'", "'mastra-code'", "'codebuddy'"} {
		if !strings.Contains(schema, harness) {
			t.Fatalf("repaired sessions constraint lost %s: %s", harness, schema)
		}
	}
	if strings.Count(schema, "'codebuddy'") != 1 || !strings.Contains(schema, "'mastra-code', 'codebuddy', 'fake'))") {
		t.Fatalf("CodeBuddy Code is not once, after Mastra Code: %s", schema)
	}
	if err := reconcileHarnessConstraint(db); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
}

// Earlier published draft branches used both numbers. Releasing the collided
// ledger entry must restore main's schema without losing an existing worker.
func TestCodeBuddyPreviewMigrationCollisionsPreserveSessions(t *testing.T) {
	for _, version := range []int64{214, 215} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 212)
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''codebuddy'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, version)
			mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('preview', '/preview', CURRENT_TIMESTAMP)`)
			mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at) VALUES ('existing-codebuddy', 'preview', 1, 'codebuddy', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			for attempt := range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate attempt %d: %v", attempt, err)
				}
				var objects int
				if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' AND name IN ('managed_harness_releases', 'session_harness_releases')) OR (type = 'trigger' AND name IN ('sessions_record_harness_release_insert', 'sessions_record_harness_release_update'))`).Scan(&objects); err != nil || objects != 4 {
					t.Fatalf("main release schema objects = %d: %v", objects, err)
				}
				var refresh bool
				if err := db.QueryRow(`SELECT version_refresh FROM app_settings WHERE id = 1`).Scan(&refresh); err != nil || !refresh {
					t.Fatalf("main version refresh missing: %v %v", refresh, err)
				}
				var harness string
				if err := db.QueryRow(`SELECT harness FROM sessions WHERE id = 'existing-codebuddy'`).Scan(&harness); err != nil || harness != "codebuddy" {
					t.Fatalf("preview session changed: %q %v", harness, err)
				}
			}
		})
	}
}

func TestCodeBuddyPreviewRepairRefusesPartialCanonicalSchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 212)
	mustExec(t, db, `PRAGMA writable_schema = ON`)
	mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''codebuddy'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
	mustExec(t, db, `PRAGMA writable_schema = RESET`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (214, 1), (215, 1)`)
	mustExec(t, db, `CREATE TABLE managed_harness_releases (harness TEXT PRIMARY KEY)`)
	if err := repairRenumberedCodeBuddyMigrationHistory(db); err == nil || !strings.Contains(err.Error(), "partial schema") {
		t.Fatalf("partial canonical schema accepted: %v", err)
	}
	var retained int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id IN (214, 215) AND is_applied = 1`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("partial repair changed ledger: %d %v", retained, err)
	}
}

// Once CodeBuddy's migration is recorded, a later change to main's canonical
// 214/215 objects must not read as a preview ledger: the repair neither refuses
// startup nor releases a canonical ledger entry.
func TestCodeBuddyPreviewRepairStopsOnceItsMigrationIsRecorded(t *testing.T) {
	db := openMigratedDatabaseCopy(t, codeBuddyHarnessMigration)
	mustExec(t, db, `DROP TRIGGER sessions_record_harness_release_insert`)
	if err := repairRenumberedCodeBuddyMigrationHistory(db); err != nil {
		t.Fatalf("repair after a later canonical change: %v", err)
	}
	var retained int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id IN (214, 215) AND is_applied = 1`).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("canonical ledger entries = %d, %v; want both retained", retained, err)
	}
}

// A preview CodeBuddy number proves nothing about the 214/215 receipts, so it
// must not end the repair: a collided receipt whose canonical effects are
// absent is still released for goose to apply main's migration.
func TestCodeBuddyPreviewNumberDoesNotSkipCollidedReceiptRepair(t *testing.T) {
	for _, preview := range []int64{20261023181512240, 20261114173149181} {
		t.Run(fmt.Sprint(preview), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 212)
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, '''fake''))', '''codebuddy'', ''fake''))') WHERE type = 'table' AND name = 'sessions'`)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
			mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (214, 1), (?, 1)`, preview)
			if err := repairRenumberedCodeBuddyMigrationHistory(db); err != nil {
				t.Fatal(err)
			}
			var retained int
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 214`).Scan(&retained); err != nil || retained != 0 {
				t.Fatalf("collided 214 receipt retained = %d, %v; want released", retained, err)
			}
		})
	}
}
