package sqlite

import (
	"strconv"
	"strings"
	"testing"
)

const (
	turnLineageMigrationVersion = 20261128072745042
	beforeTurnLineageMigration  = 20261128055914527
)

// The numbers builds of this migration recorded before it moved: the draft's
// and the one it had before main's newer migrations landed below it.
var turnLineageEarlierMigrationVersions = []int64{20261101115405806, 20261126084920915}

// Break caught: reading a forked conversation checked each turn's lineage with a
// range scan of the conversation's history, so a resume of a large forked
// conversation ran past its 30s deadline (#7545). The turn_id indexes the session
// delete added are used only once the database has statistics, and AO never
// collects them, so the lookup needs an index that leads on turn_id and answers
// the whole check.
func TestConversationTurnLineageLookupsUseIndexes(t *testing.T) {
	db := openMigratedTestDB(t)
	for _, tc := range []struct{ table, index string }{
		{"conversation_messages", "idx_conversation_messages_turn_lineage"},
		{"conversation_activities", "idx_conversation_activities_turn_lineage"},
	} {
		plan := strings.Join(queryPlan(t, db, `SELECT 1 FROM `+tc.table+
			` WHERE turn_id = ? AND conversation_id = ? AND sequence <= ?`, "turn-1", "conv-1", 10), "\n")
		if !strings.Contains(plan, "INDEX "+tc.index) {
			t.Fatalf("%s lineage lookup does not use %s: %q", tc.table, tc.index, plan)
		}
	}
}

// Builds of this migration under its earlier numbers recorded those numbers, and
// the draft created full lineage indexes beside the turn_id-only ones. The
// migration applies once on such a database, leaves the indexes as on a fresh
// install, and drops the earlier history row. Startup stays repeatable.
func TestTurnLineageMigrationAdoptsDraftHistory(t *testing.T) {
	for _, earlier := range turnLineageEarlierMigrationVersions {
		t.Run(strconv.FormatInt(earlier, 10), func(t *testing.T) { assertTurnLineageAdoptsHistory(t, earlier) })
	}
}

func assertTurnLineageAdoptsHistory(t *testing.T, earlier int64) {
	db := openMigratedDatabaseCopy(t, beforeTurnLineageMigration)
	mustExec(t, db, `CREATE INDEX idx_conversation_messages_turn_lineage ON conversation_messages(turn_id, conversation_id, sequence)`)
	mustExec(t, db, `CREATE INDEX idx_conversation_activities_turn_lineage ON conversation_activities(turn_id, conversation_id, sequence)`)
	mustExec(t, db, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, earlier)
	for range 2 {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate draft history: %v", err)
		}
	}
	var draft, applied int
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?),
		(SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1)`,
		earlier, turnLineageMigrationVersion).Scan(&draft, &applied); err != nil {
		t.Fatal(err)
	}
	if draft != 0 || applied != 1 {
		t.Fatalf("earlier history rows = %d, migration receipts = %d; want 0 and 1", draft, applied)
	}
	indexes := map[string]string{}
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name IN ('conversation_messages', 'conversation_activities') AND name LIKE '%_turn%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		indexes[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"idx_conversation_messages_turn_lineage", "idx_conversation_activities_turn_lineage"} {
		if !strings.Contains(indexes[name], "WHERE turn_id IS NOT NULL") {
			t.Errorf("%s = %q, want the partial index a fresh install has", name, indexes[name])
		}
	}
	for _, name := range []string{"idx_conversation_messages_turn", "idx_conversation_activities_turn"} {
		if definition, ok := indexes[name]; ok {
			t.Errorf("%s is still there (%q); the lineage index replaces it", name, definition)
		}
	}
}
