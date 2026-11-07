package sqlite

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// Claude chats recorded a reroute notice for every alias, so session
// agent-orchestrator-117 carried seven "Answered by claude-opus-5-5 instead of opus"
// rows. The migration removes those and leaves real substitutions alone.
func TestMigrationRemovesClaudeAliasResolutionNotices(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261103094403671)
	at := time.Date(2026, time.November, 5, 8, 0, 0, 0, time.UTC)

	legacy := func(from, to string) string {
		return `{"event":"model.rerouted","fromModel":"` + from + `","toModel":"` + to +
			`","reason":"The installed Claude Code resolves \"` + from + `\" to ` + to + `"}`
	}
	record := func(from, to, reason string) string {
		return `{"fromModel":"` + from + `","toModel":"` + to + `","reason":"` + reason + `","at":"2026-11-05T08:00:00Z"}`
	}
	legacyReason := func(from, to string) string {
		return `The installed Claude Code resolves \"` + from + `\" to ` + to
	}

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	for _, tc := range []struct{ id, reroute string }{
		{"resolved", record("opus", "claude-opus-5-5", legacyReason("opus", "claude-opus-5-5"))},
		{"context", record("opus[1m]", "claude-opus-5-5", legacyReason("opus[1m]", "claude-opus-5-5"))},
		{"cross-family", record("opus", "claude-sonnet-5-5", legacyReason("opus", "claude-sonnet-5-5"))},
		{"codex", record("gpt-6.1-sol", "gpt-5.5", "High demand")},
		{"new-claude", record("opus", "claude-opus-4-8", `\"opus\" normally resolves to Opus 5.5, but claude-opus-4-8 answered this turn`)},
	} {
		mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES (?, 'p', (SELECT COUNT(*) + 1 FROM sessions), 'claude-code', 'chat', ?, ?, ?);`,
			tc.id, at, at, at)
		mustExec(t, db, `
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, active_branch_id, model_reroute_json, created_at, updated_at)
VALUES (?, 'session', 'p', ?, ?, ? || ':root', ?, ?, ?);`,
			tc.id, tc.id, tc.id, tc.id, tc.reroute, at, at)
	}

	rows := map[string]string{
		"resolved":     legacy("opus", "claude-opus-5-5"),
		"context":      legacy("opus[1m]", "claude-opus-5-5"),
		"cross-family": legacy("opus", "claude-sonnet-5-5"),
		"codex":        `{"event":"model.rerouted","fromModel":"gpt-6.1-sol","toModel":"gpt-5.5","reason":"High demand"}`,
		"new-claude":   `{"event":"model.rerouted","fromModel":"opus","toModel":"claude-opus-4-8","reason":"\"opus\" normally resolves to Opus 5.5, but claude-opus-4-8 answered this turn"}`,
	}
	for conversation, detail := range rows {
		mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, summary, detail_json, created_at, updated_at, branch_id)
VALUES (? || '-notice', ?, 1, 'system', 'completed', 'Provider answered', ?, ?, ?, ? || ':root'),
       (? || '-other', ?, 2, 'system', 'completed', 'Compacted', '{"event":"compaction"}', ?, ?, ? || ':root');`,
			conversation, conversation, detail, at, at, conversation, conversation, conversation, at, at, conversation)
	}

	upTo(t, db, 20261106103340099)

	for _, tc := range []struct {
		conversation string
		kept         bool
	}{
		{"resolved", false},
		{"context", false},
		{"cross-family", true},
		{"codex", true},
		{"new-claude", true},
	} {
		var detail string
		err := db.QueryRow(`SELECT detail_json FROM conversation_activities WHERE id = ?`, tc.conversation+"-notice").Scan(&detail)
		if kept := !errors.Is(err, sql.ErrNoRows); err != nil && kept {
			t.Fatalf("read %s notice: %v", tc.conversation, err)
		} else if kept != tc.kept {
			t.Errorf("%s timeline notice kept = %v, want %v", tc.conversation, kept, tc.kept)
		}
		var record sql.NullString
		if err := db.QueryRow(`SELECT model_reroute_json FROM conversations WHERE id = ?`, tc.conversation).Scan(&record); err != nil {
			t.Fatalf("read %s record: %v", tc.conversation, err)
		}
		if record.Valid != tc.kept {
			t.Errorf("%s conversation record kept = %v, want %v", tc.conversation, record.Valid, tc.kept)
		}
		var other int
		if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_activities WHERE id = ?`, tc.conversation+"-other").Scan(&other); err != nil || other != 1 {
			t.Errorf("%s unrelated activity count = %d, err = %v; want it untouched", tc.conversation, other, err)
		}
	}
}
