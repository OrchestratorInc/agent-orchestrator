package sqlite

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

// claude-agent-acp 0.70 failed a finished Claude answer that quoted "Please run
// /login" as a refused login, with the answer's last text block as the error
// (#8427). The migration marks those turns completed and drops the sign-in
// demand they left, and keeps real refusals, a refusal after an answer that
// quoted its text earlier, and other failures.
func TestMigrationRepairsAnswersMarkedAsRefusedLogins(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261115072651599)
	at := time.Date(2026, time.November, 14, 17, 28, 56, 0, time.UTC)
	const (
		refusal = "Not logged in · Please run /login"
		quoted  = "Those were not real logouts. Each answer quoted \"" + refusal + "\" while explaining the bug."
	)

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	for i, tc := range []struct {
		name, state, failureCode, errorMessage, answer, reauthReason string
	}{
		// The answer streamed, then the adapter failed it with its last text block.
		{"quoted", "failed", "auth_required", quoted, "Let me check the logs.\n\n" + quoted + "\n", quoted},
		// The same, from before turns carried a failure code.
		{"quoted-before-codes", "failed", "", quoted, quoted, ""},
		// Nothing streamed: Claude Code refused the login.
		{"refused", "failed", "auth_required", refusal, "", refusal},
		// The answer quoted the refusal earlier, then the login really failed.
		{"refused-after-quote", "failed", "auth_required", refusal, "The CLI said \"" + refusal + "\" earlier. Checking again.", refusal},
		// Another failure that happens to end the answer.
		{"other-failure", "failed", "usage_limit", quoted, quoted, ""},
		{"completed", "completed", "", "", quoted, ""},
	} {
		session := tc.name + "-1"
		account := sql.NullString{}
		if tc.reauthReason != "" {
			encoded, err := json.Marshal(map[string]string{
				"authenticationState": "required", "reauthRequiredAt": "2026-11-14T17:28:56Z", "reauthReason": tc.reauthReason,
				"lastAuthFailureAt": "2026-11-14T17:28:56Z", "lastAuthFailureReason": tc.reauthReason,
			})
			if err != nil {
				t.Fatal(err)
			}
			account = sql.NullString{Valid: true, String: string(encoded)}
		}
		mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES (?, 'p', ?, 'claude-code', 'chat', ?, ?, ?);`, session, i+1, at, at, at)
		mustExec(t, db, `
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, account_json, created_at, updated_at)
VALUES (?, 'session', 'p', ?, ?, ?, ?, ?);`, tc.name, session, session, account, at, at)
		mustExec(t, db, `
INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, provider_turn_id, state, error_message, failure_code, requested_at, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`, tc.name, tc.name, session, tc.name+"-provider", tc.state, tc.errorMessage, tc.failureCode, at, at)
		mustExec(t, db, `
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at)
VALUES (? || '-prompt', ?, ?, 1, 'user', 'human', 'Were those real logouts?', ?, ?);`, tc.name, tc.name, tc.name, at, at)
		if tc.answer != "" {
			mustExec(t, db, `
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at)
VALUES (? || '-answer', ?, ?, 2, 'assistant', 'provider', ?, ?, ?);`, tc.name, tc.name, tc.name, tc.answer, at, at)
		}
	}

	upTo(t, db, 20261115115851380)

	for turn, want := range map[string]struct {
		state, failureCode, errorMessage string
	}{
		"quoted":              {"completed", "", ""},
		"quoted-before-codes": {"completed", "", ""},
		"refused":             {"failed", "auth_required", refusal},
		"refused-after-quote": {"failed", "auth_required", refusal},
		"other-failure":       {"failed", "usage_limit", quoted},
		"completed":           {"completed", "", ""},
	} {
		var state, failureCode, errorMessage string
		if err := db.QueryRow(`SELECT state, failure_code, error_message FROM conversation_turns WHERE id = ?`, turn).
			Scan(&state, &failureCode, &errorMessage); err != nil {
			t.Fatalf("read %s: %v", turn, err)
		}
		if state != want.state || failureCode != want.failureCode || errorMessage != want.errorMessage {
			t.Errorf("%s = (%q, %q, %q), want (%q, %q, %q)", turn, state, failureCode, errorMessage, want.state, want.failureCode, want.errorMessage)
		}
	}

	for conversation, wantDemand := range map[string]bool{
		"quoted":              false,
		"refused":             true,
		"refused-after-quote": true,
	} {
		var demand sql.NullString
		var reason, lastReason string
		if err := db.QueryRow(`
SELECT json_extract(account_json, '$.reauthRequiredAt'),
       coalesce(json_extract(account_json, '$.authenticationState'), ''),
       json_extract(account_json, '$.lastAuthFailureReason')
FROM conversations WHERE id = ?`, conversation).Scan(&demand, &reason, &lastReason); err != nil {
			t.Fatalf("read %s account: %v", conversation, err)
		}
		if demand.Valid != wantDemand || (reason == "required") != wantDemand {
			t.Errorf("%s: sign-in demand = %v (state %q), want %v", conversation, demand.Valid, reason, wantDemand)
		}
		if lastReason == "" {
			t.Errorf("%s: the last failure's evidence was dropped", conversation)
		}
	}
}
