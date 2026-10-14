package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestMigration0197AllowsAOActionAndKeepsActivitySchema(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 196)

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

	upTo(t, db, 197)

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

	downTo(t, db, 196)
	var restored string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'conversation_activities'`).Scan(&restored); err != nil || restored != beforeTable {
		t.Fatalf("down migration schema = %q err=%v, want original", restored, err)
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
