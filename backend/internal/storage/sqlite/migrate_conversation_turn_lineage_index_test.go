package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

// Break caught: reading a forked conversation checked each turn's lineage with a
// range scan of the whole history, so a resume of a large forked conversation
// ran past its 30s deadline (#7545).
func TestConversationTurnLineageLookupsUseIndexes(t *testing.T) {
	db := openMigratedTestDB(t)
	for _, tc := range []struct{ table, index string }{
		{"conversation_messages", "idx_conversation_messages_turn_lineage"},
		{"conversation_activities", "idx_conversation_activities_turn_lineage"},
	} {
		plan := queryPlan(t, db, `SELECT 1 FROM `+tc.table+
			` WHERE turn_id = ? AND conversation_id = ? AND sequence <= ?`, "turn-1", "conv-1", 10)
		if !strings.Contains(plan, "INDEX "+tc.index) {
			t.Fatalf("%s lineage lookup does not use %s: %q", tc.table, tc.index, plan)
		}
	}
}

func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}
