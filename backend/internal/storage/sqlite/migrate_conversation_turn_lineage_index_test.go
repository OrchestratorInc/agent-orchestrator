package sqlite

import (
	"strings"
	"testing"
)

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
