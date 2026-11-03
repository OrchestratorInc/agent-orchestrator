package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestMigrationAllowsAOActionAndKeepsActivitySchema(t *testing.T) {
	const actionMigration = 20261020090353460
	db := openMigratedDatabaseCopy(t, 215)

	var beforeTable string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'conversation_activities'`).Scan(&beforeTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(beforeTable, "'user_input'") || strings.Contains(beforeTable, "'ao_action'") {
		t.Fatalf("pre-migration kind check = %s", beforeTable)
	}
	beforeIndexes := schemaObjects(t, db, "index", "idx_conversation_activities%")
	beforeTriggers := schemaObjects(t, db, "trigger", "conversation_activities_%")
	if beforeIndexes == "" || beforeTriggers == "" {
		t.Fatalf("indexes=%q triggers=%q, want both preserved", beforeIndexes, beforeTriggers)
	}

	upTo(t, db, actionMigration)

	var afterTable string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'conversation_activities'`).Scan(&afterTable); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(afterTable, "'ao_action'") {
		t.Fatalf("kind check after migration = %s", afterTable)
	}
	if got := schemaObjects(t, db, "index", "idx_conversation_activities%"); got != beforeIndexes {
		t.Fatalf("indexes changed:\n%s\n%s", beforeIndexes, got)
	}
	if got := schemaObjects(t, db, "trigger", "conversation_activities_%"); got != beforeTriggers {
		t.Fatalf("triggers changed:\n%s\n%s", beforeTriggers, got)
	}

	mustExec(t, db, `INSERT INTO projects (id, path, registered_at)
		VALUES ('action-project', '/tmp/action-project', CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at)
		VALUES ('action-session', 'action-project', 1, 'codex', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, created_at, updated_at)
		VALUES ('action-conversation', 'session', 'action-project', 'action-session', 'action-session', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO conversation_activities (id, conversation_id, sequence, kind, status, summary, created_at, updated_at)
		VALUES ('action-activity', 'action-conversation', 1, 'ao_action', 'completed', 'Spawned a session', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	downTo(t, db, 215)
	var afterDown string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'conversation_activities'`).Scan(&afterDown); err != nil || afterDown != afterTable {
		t.Fatalf("down migration schema = %q err=%v, want widened constraint retained", afterDown, err)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM conversation_activities WHERE id = 'action-activity'`).Scan(&kind); err != nil || kind != "ao_action" {
		t.Fatalf("action after rollback = %q, %v; want ao_action", kind, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after rollback = %q, %v; want ok", integrity, err)
	}
}

func TestProviderItemLookupUsesIndex(t *testing.T) {
	const actionMigration = 20261020090353460
	const lookupMigration = 20261021142844923
	db := openMigratedDatabaseCopy(t, actionMigration)
	upTo(t, db, lookupMigration)

	usesLookupIndex := func() bool {
		rows, err := db.Query(`EXPLAIN QUERY PLAN
SELECT current_session_id
FROM conversations
JOIN conversation_activities ON conversation_activities.conversation_id = conversations.id
WHERE conversation_activities.provider_item_id = ?
  AND conversation_activities.provider_item_id <> ''
  AND current_session_id IS NOT NULL
LIMIT 1`, "ao-action:session.spawned:mer-1")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		usesIndex := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			usesIndex = usesIndex ||
				strings.Contains(detail, "idx_conversation_activities_provider_item_lookup") &&
					strings.Contains(detail, "provider_item_id=?")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return usesIndex
	}()
	if !usesLookupIndex {
		t.Fatal("provider item lookup did not use its index")
	}

	downTo(t, db, actionMigration)
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_conversation_activities_provider_item_lookup'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("lookup index remains after rollback: %d", remaining)
	}
}

func schemaObjects(t *testing.T, db *sql.DB, kind, nameLike string) string {
	t.Helper()
	rows, err := db.Query(`SELECT name || ' ' || sql FROM sqlite_master WHERE type = ? AND name LIKE ? ORDER BY name`, kind, nameLike)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, sqlText)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(parts, "\n")
}
