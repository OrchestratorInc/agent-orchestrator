package sqlite

import (
	"strings"
	"testing"
)

// A database that recorded 20261103030401452 from an earlier build kept the
// narrow usage CHECKs, so agy and every other Chat harness failed to bind usage
// (agent-orchestrator-222). The repair opens both constraints and leaves a
// database that already has the open shape unchanged.
func TestRepairOpensDriftedUsageChecks(t *testing.T) {
	for _, drifted := range []bool{true, false} {
		db := openMigratedDatabaseCopy(t, 20261126154409684)
		if drifted {
			mustExec(t, db, `PRAGMA writable_schema = ON`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, 'CHECK (trim(harness) <> '''')',
				'CHECK (harness IN (''claude-code'', ''codex'', ''kimi''))') WHERE name = 'usage_bindings'`)
			mustExec(t, db, `UPDATE sqlite_master SET sql = replace(sql, 'CHECK (trim(kind) <> '''')',
				'CHECK (kind IN (''claude_main'', ''claude_subagent'', ''codex_rollout'', ''kimi_wire''))') WHERE name = 'usage_sources'`)
			mustExec(t, db, `PRAGMA writable_schema = RESET`)
		}
		var before string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'usage_bindings'`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if drifted != strings.Contains(before, "CHECK (harness IN (") {
			t.Fatalf("drifted=%v but usage_bindings is %s", drifted, before)
		}

		if err := migrate(db); err != nil {
			t.Fatalf("migrate drifted=%v: %v", drifted, err)
		}
		for table, open := range map[string]string{
			"usage_bindings": "CHECK (trim(harness) <> '')",
			"usage_sources":  "CHECK (trim(kind) <> '')",
		} {
			var schema string
			if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, table).Scan(&schema); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(schema, open) || strings.Contains(schema, " IN ('claude") {
				t.Fatalf("drifted=%v %s = %s", drifted, table, schema)
			}
		}

		mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('p','/tmp/p',CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at)
			VALUES ('p-1','p',1,'agy',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO usage_bindings(session_id,harness,native_root_id,state,updated_at)
			VALUES ('p-1','agy','root','discovering',CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO usage_sources(binding_id,kind,artifact_path,state,updated_at)
			VALUES ((SELECT id FROM usage_bindings WHERE session_id='p-1'),'agy_transcript','/tmp/t','pending',CURRENT_TIMESTAMP)`)
		if _, err := db.Exec(`INSERT INTO usage_bindings(session_id,harness,native_root_id,state,updated_at)
			VALUES ('p-1','  ','root-2','discovering',CURRENT_TIMESTAMP)`); err == nil {
			t.Fatal("blank harness accepted: the CHECK was removed instead of opened")
		}
		var integrity string
		if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
			t.Fatalf("integrity %s: %v", integrity, err)
		}
	}
}
