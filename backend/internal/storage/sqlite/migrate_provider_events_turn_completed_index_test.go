package sqlite

import (
	"fmt"
	"strings"
	"testing"
)

func TestMigrationIndexesOnlyTurnCompletedProviderEvents(t *testing.T) {
	const index = "idx_conversation_provider_events_turn_completed"
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
