package sqlite

import (
	"slices"
	"testing"
)

func TestMigrationSessionAcceptedDeliveryUpDown(t *testing.T) {
	const before, version = 20261114072016988, 20261114182101000
	db := openMigratedDatabaseCopyNoForeignKeys(t, before)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at)
		VALUES ('accepted-session', 'accepted-project', 1, 'claude-code', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)

	upTo(t, db, version)
	var deliveryID, launchID string
	if err := db.QueryRow(`SELECT accepted_delivery_id, accepted_delivery_launch_id FROM sessions WHERE id = 'accepted-session'`).
		Scan(&deliveryID, &launchID); err != nil || deliveryID != "" || launchID != "" {
		t.Fatalf("accepted delivery after up = %q, %q, %v; want empty", deliveryID, launchID, err)
	}

	downTo(t, db, before)
	columns := tableColumns(t, db, "sessions")
	if slices.Contains(columns, "accepted_delivery_id") || slices.Contains(columns, "accepted_delivery_launch_id") {
		t.Fatalf("accepted delivery columns survived down: %v", columns)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'accepted-session'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("session after down = %d, %v; want 1", count, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after down = %q, %v", integrity, err)
	}
	upTo(t, db, version)
}
