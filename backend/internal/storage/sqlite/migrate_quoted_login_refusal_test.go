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
// quoted its text earlier, after an earlier message that ended on it or after a
// tool call that followed it, and other failures.
func TestMigrationRepairsAnswersMarkedAsRefusedLogins(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261115072651599)
	at := time.Date(2026, time.November, 14, 17, 28, 56, 0, time.UTC)
	const (
		refusal = "Not logged in · Please run /login"
		quoted  = "Those were not real logouts. Each answer quoted \"" + refusal + "\" while explaining the bug."
	)

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	for i, tc := range []struct {
		name, state, failureCode, errorMessage string
		answers                                []string
		reauthReason                           string
	}{
		// The answer streamed, then the adapter failed it with its last text block.
		{"quoted", "failed", "auth_required", quoted, []string{"Let me check the logs.\n\n" + quoted + "\n"}, quoted},
		// The same, from before turns carried a failure code.
		{"quoted-before-codes", "failed", "", quoted, []string{quoted}, ""},
		// The result spaced the answer's text differently than it streamed.
		{"quoted-spacing", "failed", "auth_required", "The CLI printed:\n" + refusal, []string{"The CLI printed:\n\n" + refusal}, ""},
		// The answer's refusal text has a non-breaking space where the error has a
		// plain one.
		{"quoted-nbsp", "failed", "auth_required", refusal, []string{"The CLI printed:\n\nNot logged in ·\u00a0Please run /login"}, ""},
		// Nothing streamed: Claude Code refused the login.
		{"refused", "failed", "auth_required", refusal, nil, refusal},
		// The answer quoted the refusal earlier, then the login really failed.
		{"refused-after-quote", "failed", "auth_required", refusal, []string{"The CLI said \"" + refusal + "\" earlier. Checking again."}, refusal},
		// An earlier message ended on the refusal text, the agent went on, then
		// the login really failed.
		{"refused-after-quoted-message", "failed", "auth_required", refusal, []string{"The CLI printed:\n\n" + refusal, "Let me check again."}, refusal},
		// The answer ended on the refusal text, a tool call followed (recorded
		// below), then the login really failed.
		{"refused-after-tool-call", "failed", "auth_required", refusal, []string{"The CLI printed:\n\n" + refusal}, refusal},
		// Another failure that happens to end the answer.
		{"other-failure", "failed", "usage_limit", quoted, []string{quoted}, ""},
		{"completed", "completed", "", "", []string{quoted}, ""},
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
		for j, answer := range tc.answers {
			mustExec(t, db, `
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at)
VALUES (? || '-answer-' || ?, ?, ?, ?, 'assistant', 'provider', ?, ?, ?);`, tc.name, j, tc.name, tc.name, j+2, answer, at, at)
		}
	}
	mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, turn_id, sequence, kind, status, created_at, updated_at)
VALUES ('refused-after-tool-call-tool', 'refused-after-tool-call', 'refused-after-tool-call', 3, 'command', 'completed', ?, ?);`, at, at)

	upTo(t, db, 20261115115851380)

	for turn, want := range map[string]struct {
		state, failureCode, errorMessage string
	}{
		"quoted":                       {"completed", "", ""},
		"quoted-before-codes":          {"completed", "", ""},
		"quoted-spacing":               {"completed", "", ""},
		"quoted-nbsp":                  {"completed", "", ""},
		"refused":                      {"failed", "auth_required", refusal},
		"refused-after-quote":          {"failed", "auth_required", refusal},
		"refused-after-quoted-message": {"failed", "auth_required", refusal},
		"refused-after-tool-call":      {"failed", "auth_required", refusal},
		"other-failure":                {"failed", "usage_limit", quoted},
		"completed":                    {"completed", "", ""},
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
		"quoted":                       false,
		"refused":                      true,
		"refused-after-quote":          true,
		"refused-after-quoted-message": true,
		"refused-after-tool-call":      true,
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

// An answer that ended on the CLI's refusal text was repaired, but a later turn
// in the same chat really was refused with that same text. The demand belongs to
// the later refusal and stays.
func TestMigrationKeepsLaterRefusalWithTheSameReason(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261115072651599)
	first := time.Date(2026, time.November, 14, 17, 28, 56, 0, time.UTC)
	later := first.Add(time.Hour)
	const refusal = "Not logged in · Please run /login"

	account, err := json.Marshal(map[string]string{
		"authenticationState": "required", "reauthRequiredAt": later.Format(time.RFC3339), "reauthReason": refusal,
		"lastAuthFailureAt": later.Format(time.RFC3339), "lastAuthFailureReason": refusal,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, first)
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES ('s', 'p', 1, 'claude-code', 'chat', ?, ?, ?);`, later, first, later)
	mustExec(t, db, `
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, account_json, created_at, updated_at)
VALUES ('c', 'session', 'p', 's', 's', ?, ?, ?);`, string(account), first, later)
	for i, turn := range []struct {
		id, answer string
		at         time.Time
	}{
		{"quoted", "The CLI printed this:\n\n" + refusal, first},
		{"refused", "", later},
	} {
		mustExec(t, db, `
INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, provider_turn_id, state, error_message, failure_code, requested_at, completed_at)
VALUES (?, 'c', 's', ? || '-provider', 'failed', ?, 'auth_required', ?, ?);`, turn.id, turn.id, refusal, turn.at, turn.at)
		if turn.answer != "" {
			mustExec(t, db, `
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at)
VALUES (? || '-answer', 'c', ?, ?, 'assistant', 'provider', ?, ?, ?);`, turn.id, turn.id, i+1, turn.answer, turn.at, turn.at)
		}
	}

	upTo(t, db, 20261115115851380)

	for turn, wantState := range map[string]string{"quoted": "completed", "refused": "failed"} {
		var state string
		if err := db.QueryRow(`SELECT state FROM conversation_turns WHERE id = ?`, turn).Scan(&state); err != nil {
			t.Fatalf("read %s: %v", turn, err)
		}
		if state != wantState {
			t.Errorf("%s state = %q, want %q", turn, state, wantState)
		}
	}
	var demand sql.NullString
	var reason string
	if err := db.QueryRow(`
SELECT json_extract(account_json, '$.reauthRequiredAt'), coalesce(json_extract(account_json, '$.reauthReason'), '')
FROM conversations WHERE id = 'c'`).Scan(&demand, &reason); err != nil {
		t.Fatal(err)
	}
	if !demand.Valid || reason != refusal {
		t.Errorf("sign-in demand = (%v, %q), want the later refusal's demand kept", demand.Valid, reason)
	}
}
