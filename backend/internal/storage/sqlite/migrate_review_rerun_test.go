package sqlite

import "testing"

// TestMigration0177AllowsRepeatApprovalsButNotDoubleRunning guards both halves
// of the narrowed review_run key: an explicit rerun may record a second
// approved pass for the same (worker, PR, commit, reviewer), while two running
// passes for that key still collide (#242). Down must collapse the repeat
// approvals so the wider index can be rebuilt.
func TestMigration0177AllowsRepeatApprovalsButNotDoubleRunning(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 176)
	upTo(t, db, 177)

	insert := func(id, status, verdict, createdAt string) error {
		_, err := db.Exec(
			`INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
			 VALUES (?, 'rev-1', 's1', 'claude-code', 'pr1', 'sha1', ?, ?, '', ?)`,
			id, status, verdict, createdAt,
		)
		return err
	}
	if err := insert("r-approved-1", "complete", "approved", "2026-06-01T00:00:00Z"); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if err := insert("r-approved-2", "complete", "approved", "2026-06-02T00:00:00Z"); err != nil {
		t.Fatalf("a rerun's approval must be recordable: %v", err)
	}
	if err := insert("r-running-1", "running", "", "2026-06-03T00:00:00Z"); err != nil {
		t.Fatalf("first running pass: %v", err)
	}
	if err := insert("r-running-2", "running", "", "2026-06-04T00:00:00Z"); err == nil {
		t.Fatal("two running passes for one key must still collide")
	}

	downTo(t, db, 176)
	var survivors []string
	rows, err := db.Query(`SELECT id FROM review_run ORDER BY id`)
	if err != nil {
		t.Fatalf("query survivors: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan survivor: %v", err)
		}
		survivors = append(survivors, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate survivors: %v", err)
	}
	// The wider key allows one running-or-approved row per key: the newest
	// completed approval wins over both the older approval and the running pass.
	if len(survivors) != 1 || survivors[0] != "r-approved-2" {
		t.Fatalf("survivors after down = %v, want only the newest approval", survivors)
	}
}
