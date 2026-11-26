package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

// TestAntigravityAccountsMigrationAcceptsTheProvider: after the upgrade every
// account table takes Antigravity accounts, and still refuses an unknown
// provider.
func TestAntigravityAccountsMigrationAcceptsTheProvider(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseURI(dataDir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('p1','/tmp/p1',?)`, now)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at) VALUES ('s1','p1',1,'agy',?,?,?)`, now, now, now)
	mustExec(t, db, `INSERT INTO agent_accounts (id, provider, home, created_at) VALUES ('a1', 'antigravity', '/accounts/antigravity/a1', ?)`, now)
	mustExec(t, db, `INSERT INTO agent_account_defaults (provider, account_id) VALUES ('antigravity', 'a1')`)
	mustExec(t, db, `INSERT INTO session_agent_accounts (session_id, provider, account_id, bound_at) VALUES ('s1', 'antigravity', 'a1', ?)`, now)
	mustExec(t, db, `INSERT INTO agent_limit_readings (account_id, provider, window_id, used_percent, read_at_ms) VALUES ('a1', 'antigravity', 'w', 1, 1)`)

	if _, err := db.Exec(`INSERT INTO agent_accounts (id, provider, home, created_at) VALUES ('a2', 'unknown', '/accounts/unknown/a2', ?)`, now); err == nil {
		t.Fatal("an unknown provider was accepted: the provider CHECK is gone")
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %s: %v", integrity, err)
	}
}

// TestAntigravityAccountsMigrationKeepsExistingSessionsOnTheComputersLogin:
// a session from before Antigravity accounts ran on the computer's own agy
// login, so the upgrade records that, as 0210 did for Claude and Codex, and a
// default picked later does not move it. The rollback removes those records.
func TestAntigravityAccountsMigrationKeepsExistingSessionsOnTheComputersLogin(t *testing.T) {
	const before, version = 20261124180938781, 20261126090000000
	db := openMigratedDatabaseCopy(t, before)
	created := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects(id,path,registered_at) VALUES ('p1','/tmp/p1',?)`, created)
	mustExec(t, db, `INSERT INTO sessions(id,project_id,num,harness,created_at,updated_at,activity_last_at) VALUES ('old-agy','p1',1,'agy',?,?,?)`, created, created, created)

	upTo(t, db, version)
	var account string
	var boundAt time.Time
	if err := db.QueryRow(`SELECT account_id, bound_at FROM session_agent_accounts WHERE session_id = 'old-agy' AND provider = 'antigravity'`).Scan(&account, &boundAt); err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if account != "device-antigravity" || !boundAt.Equal(created) {
		t.Fatalf("binding = %s at %v, want device-antigravity at the session's creation %v", account, boundAt, created)
	}

	downTo(t, db, before)
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_agent_accounts WHERE provider = 'antigravity'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("antigravity bindings after rollback = %d, %v", rows, err)
	}
	if _, err := db.Exec(`INSERT INTO agent_account_defaults (provider, account_id) VALUES ('antigravity', 'a1')`); err == nil {
		t.Fatal("the rollback kept the antigravity provider in the CHECK")
	}
}
