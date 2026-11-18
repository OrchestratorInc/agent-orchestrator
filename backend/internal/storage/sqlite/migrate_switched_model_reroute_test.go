package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

// A chat switched from Claude Code to Codex before switches cleared the model
// reroute kept Claude's, so its composer still named claude-sonnet-5-5. The
// migration clears a reroute that a later provider switch replaced, and keeps one
// the current provider recorded, one in a chat that never switched, and one it
// cannot place in the timeline.
func TestMigrationClearsModelReroutesFromBeforeAProviderSwitch(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261111145612313)
	at := time.Date(2026, time.November, 13, 5, 43, 0, 0, time.UTC)
	reroute := `{"fromModel":"sonnet","toModel":"claude-sonnet-5-5","at":"2026-11-13T05:43:27Z"}`
	rerouted := `{"event":"model.rerouted","fromModel":"sonnet","toModel":"claude-sonnet-5-5"}`
	switched := `{"event":"provider_switch","sourceHarness":"claude-code","targetHarness":"codex"}`

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	for i, conversation := range []string{"switched", "rerouted-after", "unswitched", "unplaced"} {
		session := conversation + "-1"
		mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES (?, 'p', ?, 'codex', 'chat', ?, ?, ?);`, session, i+1, at, at, at)
		mustExec(t, db, `
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, model_reroute_json, created_at, updated_at)
VALUES (?, 'session', 'p', ?, ?, ?, ?, ?);`, conversation, session, session, reroute, at, at)
	}
	activity := func(conversation string, sequence int, detail string) {
		t.Helper()
		mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, summary, detail_json, created_at, updated_at)
VALUES (? || '-' || ?, ?, ?, 'system', 'completed', '', ?, ?, ?);`,
			conversation, sequence, conversation, sequence, detail, at, at)
	}
	// Claude rerouted, then the chat switched to Codex.
	activity("switched", 3, rerouted)
	activity("switched", 7, switched)
	// The chat switched, then the new provider rerouted.
	activity("rerouted-after", 3, switched)
	activity("rerouted-after", 7, rerouted)
	activity("unswitched", 3, rerouted)
	// A switch, but no timeline row says when the reroute happened.
	activity("unplaced", 7, switched)

	upTo(t, db, 20261113111818788)

	for conversation, wantCleared := range map[string]bool{
		"switched":       true,
		"rerouted-after": false,
		"unswitched":     false,
		"unplaced":       false,
	} {
		var stored sql.NullString
		if err := db.QueryRow(`SELECT model_reroute_json FROM conversations WHERE id = ?`, conversation).Scan(&stored); err != nil {
			t.Fatalf("read %s reroute: %v", conversation, err)
		}
		if cleared := !stored.Valid; cleared != wantCleared {
			t.Errorf("%s: reroute cleared = %v (stored %q), want %v", conversation, cleared, stored.String, wantCleared)
		}
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_activities WHERE json_extract(detail_json, '$.event') = 'model.rerouted'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("reroute timeline rows = %d, want all 3 kept", rows)
	}
}
