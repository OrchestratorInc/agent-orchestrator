package sqlite

import (
	"strings"
	"testing"
)

// Break caught: a session delete's cascade into conversation_provider_events
// full-scanned the table, holding the writer for ~30s on large databases.
func TestProviderEventsSessionLookupUsesIndex(t *testing.T) {
	db := openMigratedTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM conversation_provider_events WHERE session_id = ?`, "s-1")
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
	if !strings.Contains(strings.Join(plan, "\n"), "INDEX idx_conversation_provider_events_session") {
		t.Fatalf("session_id lookup does not use idx_conversation_provider_events_session: %q", plan)
	}
}
