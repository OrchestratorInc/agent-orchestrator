package sqlite

import (
	"testing"
	"time"
)

func TestMigration0172PreservesManagedAndNativeIntent(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 171)
	now := time.Now().UTC()
	mustExec(t, db, `INSERT INTO projects (id,path,display_name,registered_at) VALUES ('bindings','/tmp/bindings','bindings',?)`, now)
	for i, harness := range []string{"codex", "codex", "claude-code", "fake"} {
		mustExec(t, db, `INSERT INTO sessions (id,project_id,num,harness,activity_last_at,created_at,updated_at) VALUES (?, 'bindings', ?, ?, ?, ?, ?)`, "session-"+string(rune('a'+i)), i+1, harness, now, now, now)
	}
	mustExec(t, db, `INSERT INTO accounts_manager_session_routes (session_id,provider,account_id,created_at,updated_at) VALUES ('session-a','codex','selected-account',?,?)`, now, now)
	upTo(t, db, 172)
	rows, err := db.Query(`SELECT session_id,provider,connection_mode,account_id,revision FROM accounts_manager_session_bindings ORDER BY session_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{"session-a": "codex/managed/selected-account", "session-b": "codex/native/", "session-c": "claude/native/"}
	for rows.Next() {
		var id, provider, mode, account string
		var revision int64
		if err := rows.Scan(&id, &provider, &mode, &account, &revision); err != nil {
			t.Fatal(err)
		}
		if provider+"/"+mode+"/"+account != want[id] || revision != 1 {
			t.Fatal("migration changed account intent")
		}
		delete(want, id)
	}
	if rows.Err() != nil || len(want) != 0 {
		t.Fatal("migration omitted a binding")
	}
}
