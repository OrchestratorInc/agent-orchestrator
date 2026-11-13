package sqlite

import (
	"testing"
	"time"
)

func TestMigrationPreservesCompletedPRClaims(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261113111818788)
	at := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', ?);`, at)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at) VALUES ('p-1', 'p', 1, 'codex', ?, ?, ?);`, at, at, at)
	mustExec(t, db, `INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, created_at, updated_at) VALUES ('c', 'session', 'p', 'p-1', 'p-1', ?, ?);`, at, at)
	for i, url := range []string{"claimed", "automatic", "failed", "alias", "automated-claim", "relayed-claim"} {
		mustExec(t, db, `INSERT INTO pr (url, session_id, number, pr_state, review_decision, ci_state, mergeability, updated_at) VALUES (?, 'p-1', ?, 'open', 'none', 'unknown', 'unknown', ?);`, url, i+1, at)
	}
	mustExec(t, db, `INSERT INTO pr_url_alias (alias_url, canonical_url) VALUES ('previous', 'alias');`)
	for i, detail := range []string{
		`{"action":"pull_request.claimed","targetSessionId":"p-1","href":"claimed"}`,
		`{"action":"pull_request.claimed","targetSessionId":"p-2","href":"automatic"}`,
		`{"action":"pull_request.claimed","targetSessionId":"p-1","href":"failed"}`,
		`{"action":"pull_request.claimed","targetSessionId":"p-1","href":"previous"}`,
		`invalid JSON`,
		`{"action":"pull_request.claimed","targetSessionId":"p-1","href":"automated-claim"}`,
		`{"action":"pull_request.claimed","targetSessionId":"p-1","href":"relayed-claim"}`,
	} {
		status := "completed"
		if i == 2 {
			status = "failed"
		}
		mustExec(t, db, `INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, detail_json, created_at, updated_at) VALUES (?, 'c', ?, 'ao_action', ?, ?, ?, ?);`, detail, i+1, status, detail, at, at)
	}
	for i, url := range []string{"automated-claim", "relayed-claim"} {
		mustExec(t, db, `UPDATE conversation_activities SET turn_id = ? WHERE json_valid(detail_json) AND json_extract(detail_json, '$.href') = ?;`, url, url)
		mustExec(t, db, `INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, authored_by_user, created_at, updated_at) VALUES (?, 'c', ?, ?, 'user', 'automation', 'message', ?, ?, ?);`, url, url, i+10, i, at, at)
	}

	upTo(t, db, 20261113152109579)
	for url, want := range map[string]string{"claimed": "claimed", "automatic": "", "failed": "", "alias": "claimed", "automated-claim": "", "relayed-claim": "claimed"} {
		var got string
		if err := db.QueryRow(`SELECT ownership_source FROM pr WHERE url = ?`, url).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s source=%q, want %q", url, got, want)
		}
	}
}
