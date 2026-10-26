package sqlite

import (
	"testing"
	"time"
)

// TestMigrationAgentAccountsKeepsExistingSessionsOnTheComputersLogin upgrades a
// database from before accounts. Sessions that already existed ran on the
// computer's own login, so they are bound to it rather than reading as whatever
// default is picked later; an interface switch recorded before the account
// columns still reads back, and a same-mode row must now name an account.
func TestMigrationAgentAccountsKeepsExistingSessionsOnTheComputersLogin(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 200)
	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p1', '/tmp/p1', 'proj', ?)`, created); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for n, id := range []string{"old-chat", "old-terminal"} {
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
			VALUES (?, 'p1', ?, 'worker', 'idle', ?, 0, ?, ?)`, id, n+1, created, created, created); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO session_interface_transitions (id, session_id, source_mode, target_mode, policy, phase, created_at, updated_at)
		VALUES ('switch-1', 'old-terminal', 'tui', 'chat', 'drain', 'completed', ?, ?)`, created, created); err != nil {
		t.Fatalf("seed transition: %v", err)
	}

	upTo(t, db, 211)

	rows, err := db.Query(`SELECT session_id, provider, account_id, bound_at FROM session_agent_accounts ORDER BY session_id, provider`)
	if err != nil {
		t.Fatalf("query bindings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var session, provider, account string
		var boundAt time.Time
		if err := rows.Scan(&session, &provider, &account, &boundAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !boundAt.Equal(created) {
			t.Fatalf("%s/%s bound at %v, want the session's creation %v", session, provider, boundAt, created)
		}
		got = append(got, session+"/"+provider+"="+account)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read bindings: %v", err)
	}
	want := []string{
		"old-chat/claude=device-claude", "old-chat/codex=device-codex",
		"old-terminal/claude=device-claude", "old-terminal/codex=device-codex",
	}
	if len(got) != len(want) {
		t.Fatalf("bindings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bindings = %v, want %v", got, want)
		}
	}

	var source, target, sourceAccount, targetAccount string
	if err := db.QueryRow(`SELECT source_mode, target_mode, source_account_id, target_account_id FROM session_interface_transitions WHERE id = 'switch-1'`).
		Scan(&source, &target, &sourceAccount, &targetAccount); err != nil {
		t.Fatalf("read old transition: %v", err)
	}
	if source != "tui" || target != "chat" || sourceAccount != "" || targetAccount != "" {
		t.Fatalf("old transition = %s->%s accounts %q->%q", source, target, sourceAccount, targetAccount)
	}
	if _, err := db.Exec(`INSERT INTO session_interface_transitions (id, session_id, source_mode, target_mode, policy, phase, created_at, updated_at)
		VALUES ('same-mode', 'old-chat', 'chat', 'chat', 'drain', 'requested', ?, ?)`, created, created); err == nil {
		t.Fatal("a same-mode transition without an account was accepted after the upgrade")
	}
	if _, err := db.Exec(`INSERT INTO session_interface_transitions (id, session_id, source_mode, target_mode, policy, phase, source_account_id, target_account_id, created_at, updated_at)
		VALUES ('account-move', 'old-chat', 'chat', 'chat', 'drain', 'requested', 'device-claude', 'acct-b', ?, ?)`, created, created); err != nil {
		t.Fatalf("account move rejected after the upgrade: %v", err)
	}
}
