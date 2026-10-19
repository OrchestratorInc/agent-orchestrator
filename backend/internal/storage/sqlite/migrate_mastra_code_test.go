package sqlite

import (
	"strings"
	"testing"
)

func TestMastraMigrationAndBurnedHistoryRepair(t *testing.T) {
	for _, burned := range []bool{false, true} {
		db := openMigratedDatabaseCopy(t, 164)
		if burned {
			for _, version := range []int{165, 194, 206} {
				mustExec(t, db, `INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,1)`, version)
			}
		}
		if err := migrate(db); err != nil {
			t.Fatal(err)
		}
		var schema string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='sessions'`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		for _, harness := range []string{"mastra-code", "mimo-code", "gemini", "deepseek-harness", "openhands", "command-code"} {
			if !strings.Contains(schema, "'"+harness+"'") {
				t.Fatalf("repair lost %s", harness)
			}
		}
		mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('mastra','/tmp/mastra',CURRENT_TIMESTAMP)`)
		insert := `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at) VALUES (?,'mastra',?, ?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
		mustExec(t, db, insert, "mastra-1", 1, "mastra-code")
		if _, err := db.Exec(insert, "unknown", 2, "unknown-mastra"); err == nil {
			t.Fatal("harness CHECK disabled")
		}
		if err := reconcileHarnessConstraint(db); err != nil {
			t.Fatal(err)
		}
		var integrity string
		if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
			t.Fatalf("integrity %s: %v", integrity, err)
		}
	}
}

func TestMastraFormer213LedgerRestoresCanonicalStaleBinarySchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 212)
	// A previous Mastra build applied its harness widening under version 213.
	mustExec(t, db, `INSERT INTO goose_db_version(version_id,is_applied) VALUES (213,1)`)
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"stale_binary_since", "stale_binary_version", "stale_binary_launch", "stale_binary_switch_failed_at"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("canonical 213 column %s count=%d: %v", column, count, err)
		}
	}
	var triggers int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='sessions_stale_binary_cdc_update'`).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("canonical 213 CDC trigger count=%d: %v", triggers, err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("second startup: %v", err)
	}
}
