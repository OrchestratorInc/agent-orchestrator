package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

// TestMigrationIndexesOnlyTurnCompletedProviderEvents covers the partial
// index that keeps the completed-turn authentication lookup off the full
// provider-event archive: it must pick up the rows that predate it, must not
// cover any other method, and must drop cleanly on the way down.
func TestMigrationIndexesOnlyTurnCompletedProviderEvents(t *testing.T) {
	const index = "idx_conversation_provider_events_turn_completed"
	// The archive rows are seeded without their conversation and session parents:
	// the migration only reads the table, so the parent chain adds nothing.
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261021230230412)
	for i, method := range []string{"reasoning.delta", "turn.completed", "agent_message.delta", "turn.completed", "turn.completed"} {
		mustExec(t, db, `INSERT INTO conversation_provider_events
			(conversation_id, session_id, provider_event_id, method, payload_json, received_at, branch_id)
			VALUES ('c', 's', ?, ?, '{}', CURRENT_TIMESTAMP, 'c:root')`, fmt.Sprintf("event-%d", i), method)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil || count != 0 {
		t.Fatalf("index before migration = %d, err = %v; want 0", count, err)
	}

	upTo(t, db, 20261022144835405)

	if err := db.QueryRow(
		`SELECT COUNT(*) FROM conversation_provider_events INDEXED BY ` + index + ` WHERE method = 'turn.completed'`,
	).Scan(&count); err != nil || count != 3 {
		t.Fatalf("turn.completed rows reachable through the index = %d, err = %v; want the 3 that predate it", count, err)
	}
	// A partial index cannot answer a query that does not imply its predicate, so
	// SQLite refuses to force it: that refusal is how this proves the other
	// methods were left out of the index.
	err := db.QueryRow(
		`SELECT COUNT(*) FROM conversation_provider_events INDEXED BY ` + index + ` WHERE method = 'reasoning.delta'`,
	).Scan(&count)
	if err == nil || !strings.Contains(err.Error(), "no query solution") {
		t.Fatalf("forcing the index for another method = %v; want a no-query-solution error", err)
	}

	downTo(t, db, 20261021230230412)

	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil || count != 0 {
		t.Fatalf("index after down migration = %d, err = %v; want 0", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_provider_events`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("archive rows after down migration = %d, err = %v; want all 5", count, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after downgrade = %q, %v", integrity, err)
	}
}
