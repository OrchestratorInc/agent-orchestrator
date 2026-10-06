package sqlite

import (
	"database/sql"
	"testing"
)

// TestMigration0189ReviewFindingsUpDown guards the schema AO's in-app review
// findings need (#6300): the finding table and its status check, the run's
// provider-post error, and the comment own-reply flag, and that Down removes
// all of them so 0188's schema is restored exactly.
func TestMigration0189ReviewFindingsUpDown(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 188)
	upTo(t, db, 189)

	columnPresent := func(table, column string) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
			t.Fatalf("inspect %s.%s: %v", table, column, err)
		}
		return n == 1
	}
	for _, c := range [][2]string{{"review_finding", "status"}, {"review_finding", "resolved_by_session_id"}, {"review_run", "provider_post_error"}, {"review_run", "delivery_skipped_reason"}, {"pr_comment", "own_reply"}} {
		if !columnPresent(c[0], c[1]) {
			t.Fatalf("up: %s.%s missing", c[0], c[1])
		}
	}
	insert := func(id string, ordinal int, status string) error {
		_, err := db.Exec(`INSERT INTO review_finding (id, run_id, session_id, ordinal, body, status, created_at)
			VALUES (?, 'run-1', 's1', ?, 'body', ?, '2026-10-06T00:00:00Z')`, id, ordinal, status)
		return err
	}
	if err := insert("f1", 1, "open"); err != nil {
		t.Fatalf("insert open finding: %v", err)
	}
	if err := insert("f2", 1, "open"); err == nil {
		t.Fatal("two findings with one ordinal in a run must collide")
	}
	if err := insert("f3", 2, "dismissed"); err == nil {
		t.Fatal("an unknown finding status must be rejected")
	}

	downTo(t, db, 188)
	var name sql.NullString
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE name = 'review_finding'`).Scan(&name); err != sql.ErrNoRows {
		t.Fatalf("down: review_finding still present (err=%v)", err)
	}
	if columnPresent("review_run", "provider_post_error") || columnPresent("review_run", "delivery_skipped_reason") || columnPresent("pr_comment", "own_reply") {
		t.Fatal("down: added columns still present")
	}
}
