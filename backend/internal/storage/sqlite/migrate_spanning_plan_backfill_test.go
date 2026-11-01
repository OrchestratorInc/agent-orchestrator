package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

// Plan copies written before the daemon marked superseded ones carry no
// spansTurns flag, so it could never mark them: standalone-26 kept two turnless
// 8/8 cards beside its turn's card. The migration flags Claude's copies from
// durable evidence and leaves each conversation's latest copy as the only one
// not superseded. Other agents' plans, and a switched chat's plans written after
// it left Claude, are per-turn plans and stay as they were.
func TestMigrationBackfillsSpanningPlanCopies(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261028124744441)
	at := func(minute int) time.Time { return time.Date(2026, time.October, 26, 22, minute, 0, 0, time.UTC) }
	steps := `[{"text":"Render","status":"in_progress"}]`
	done := `[{"text":"Render","status":"completed"}]`

	mustExec(t, db, `
INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at) VALUES
    ('claude-1', 'p', 1, 'claude-code', 'chat', ?, ?, ?),
    ('codex-1', 'p', 2, 'codex', 'chat', ?, ?, ?),
    ('switched-1', 'p', 3, 'codex', 'chat', ?, ?, ?);
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at) VALUES
    ('claude', 'session', 'p', 'claude-1', 'claude-1', 'claude:root', ?, ?),
    ('codex', 'session', 'p', 'codex-1', 'codex-1', 'codex:root', ?, ?),
    ('switched', 'session', 'p', 'switched-1', 'switched-1', 'switched:root', ?, ?);`,
		at(0), at(0), at(0), at(0), at(0), at(0), at(0), at(0), at(0), at(0),
		at(0), at(0), at(0), at(0), at(0), at(0))

	for _, conversation := range []string{"claude", "codex"} {
		mustExec(t, db, `
INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, provider_turn_id, state, requested_at, completed_at, plan_json, branch_id)
VALUES (? || '-turn', ?, ?, ? || '-p1', 'completed', ?, ?, '{"steps":`+steps+`}', ? || ':root');`,
			conversation, conversation, conversation+"-1", conversation, at(1), at(2), conversation)
		mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, turn_id, sequence, kind, status, summary, detail_json, provider_item_id, created_at, updated_at, branch_id) VALUES
    (? || '-turn-row', ?, ? || '-turn', 2, 'plan', 'completed', '', '{"event":"plan","steps":`+steps+`}', 'ao-plan-' || ? || '-p1', ?, ?, ? || ':root'),
    (? || '-late-1', ?, NULL, 5, 'plan', 'completed', '', '{"event":"plan","steps":`+done+`}', '', ?, ?, ? || ':root'),
    (? || '-late-2', ?, NULL, 6, 'plan', 'completed', '', '{"event":"plan","steps":`+done+`}', '', ?, ?, ? || ':root');`,
			conversation, conversation, conversation, conversation, at(1), at(30), conversation,
			conversation, conversation, at(9), at(9), conversation,
			conversation, conversation, at(9), at(9), conversation)
	}

	// The turn rows above were touched at minute 30, after the late rows, as settling
	// a turn or recovering a controller does. The late rows are still the newest
	// content: the turn's copy could only change until the turn ended, at minute 2.

	// Claude until the switch at sequence 10, Codex after it.
	mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, turn_id, sequence, kind, status, summary, detail_json, provider_item_id, created_at, updated_at, branch_id) VALUES
    ('switched-claude', 'switched', NULL, 5, 'plan', 'completed', '', '{"event":"plan","steps":`+steps+`}', '', ?, ?, 'switched:root'),
    ('switched-marker', 'switched', NULL, 10, 'system', 'completed', 'Switched', '{"event":"provider_switch","sourceHarness":"claude-code","targetHarness":"codex"}', 'switch-1', ?, ?, 'switched:root'),
    ('switched-codex', 'switched', NULL, 15, 'plan', 'completed', '', '{"event":"plan","steps":`+steps+`}', '', ?, ?, 'switched:root');`,
		at(1), at(1), at(3), at(3), at(4), at(4))

	// A Claude chat on its root branch, with an edited branch it left that holds a
	// newer copy. Only the active path is settled: the root's copy stays current.
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES ('forked-1', 'p', 4, 'claude-code', 'chat', ?, ?, ?);`, at(0), at(0), at(0))
	mustExec(t, db, `
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at)
VALUES ('forked', 'session', 'p', 'forked-1', 'forked-1', 'forked:root', ?, ?);`, at(0), at(0))
	mustExec(t, db, `
INSERT INTO conversation_branches (id, conversation_id, session_id, provider_conversation_id, parent_branch_id, fork_after_sequence, created_at)
VALUES ('forked:edit', 'forked', 'forked-1', 'thread-edit', 'forked:root', 1, ?);`, at(0))
	mustExec(t, db, `
INSERT INTO conversation_activities (id, conversation_id, turn_id, sequence, kind, status, summary, detail_json, provider_item_id, created_at, updated_at, branch_id) VALUES
    ('forked-root', 'forked', NULL, 2, 'plan', 'completed', '', '{"event":"plan","steps":`+steps+`}', '', ?, ?, 'forked:root'),
    ('forked-edit', 'forked', NULL, 3, 'plan', 'completed', '', '{"event":"plan","steps":`+done+`}', '', ?, ?, 'forked:edit');`,
		at(5), at(5), at(6), at(6))

	upTo(t, db, 20261101004244943)

	type flags struct{ spans, superseded bool }
	row := func(id string) flags {
		t.Helper()
		spans, superseded := readPlanFlags(t, db, "conversation_activities", "detail_json", id)
		return flags{spans, superseded}
	}
	turn := func(id string) flags {
		t.Helper()
		spans, superseded := readPlanFlags(t, db, "conversation_turns", "plan_json", id)
		return flags{spans, superseded}
	}
	for name, test := range map[string]struct{ got, want flags }{
		"claude turn plan":       {turn("claude-turn"), flags{spans: true, superseded: true}},
		"claude turn row":        {row("claude-turn-row"), flags{spans: true, superseded: true}},
		"claude older late row":  {row("claude-late-1"), flags{spans: true, superseded: true}},
		"claude latest late row": {row("claude-late-2"), flags{spans: true}},
		"codex turn plan":        {turn("codex-turn"), flags{}},
		"codex turn row":         {row("codex-turn-row"), flags{}},
		"codex late rows":        {row("codex-late-1"), flags{}},
		"switched claude row":    {row("switched-claude"), flags{spans: true}},
		"switched codex row":     {row("switched-codex"), flags{}},
		"forked active root row": {row("forked-root"), flags{spans: true}},
		"forked inactive edit":   {row("forked-edit"), flags{spans: true}},
	} {
		if test.got != test.want {
			t.Errorf("%s: spansTurns=%v superseded=%v, want spansTurns=%v superseded=%v",
				name, test.got.spans, test.got.superseded, test.want.spans, test.want.superseded)
		}
	}
}

// readPlanFlags reads the spansTurns and superseded marks on one stored plan.
func readPlanFlags(t *testing.T, db *sql.DB, table, column, id string) (spans, superseded bool) {
	t.Helper()
	var spansValue, supersededValue sql.NullBool
	query := `SELECT json_extract(` + column + `, '$.spansTurns'), json_extract(` + column + `, '$.superseded') FROM ` + table + ` WHERE id = ?`
	if err := db.QueryRow(query, id).Scan(&spansValue, &supersededValue); err != nil {
		t.Fatalf("read plan flags of %s: %v", id, err)
	}
	return spansValue.Bool, supersededValue.Bool
}
