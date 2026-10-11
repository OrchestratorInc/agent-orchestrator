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
		for _, harness := range []string{"mastra-code", "fx", "opencode-v2", "mimo-code", "gemini", "deepseek-harness", "openhands", "command-code"} {
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
