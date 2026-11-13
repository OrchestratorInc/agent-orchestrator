package sqlite

import (
	"database/sql"
	"testing"
	"time"
)

// Legacy review runs map onto the state machine: complete and delivered become
// completed, a superseded failure becomes superseded, failure and cancel text
// move out of body into reason codes, and running rows stay running with the
// launch that served them so the startup sweep can judge them.
func TestMigrationMapsLegacyReviewRunsOntoStateMachine(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 20261114072016988)
	at := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)

	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p', '/tmp/p', 'P', ?);`, at)
	mustExec(t, db, `
INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at)
VALUES ('w-1', 'p', 1, 'claude-code', ?, ?, ?);`, at, at, at)
	mustExec(t, db, `
INSERT INTO review (id, session_id, project_id, harness, pr_url, reviewer_handle_id, reviewer_launch_id, created_at, updated_at)
VALUES ('rev-1', 'w-1', 'p', 'claude-code', '', 'review-w-1', 'launch-7', ?, ?);`, at, at)
	insert := func(id, sha, status, verdict, body string) {
		t.Helper()
		mustExec(t, db, `
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
VALUES (?, 'rev-1', 'w-1', 'claude-code', 'https://github.com/o/r/pull/1', ?, ?, ?, ?, ?);`, id, sha, status, verdict, body, at)
	}
	insert("complete", "s1", "complete", "approved", "looks good")
	insert("delivered", "s2", "delivered", "changes_requested", "fix it")
	insert("superseded", "s3", "failed", "", "superseded by a review trigger for a newer commit")
	insert("exited", "s4", "failed", "", "reviewer process exited before submitting a result")
	insert("chat-turn", "s5", "failed", "", "reviewer Chat turn ended without submitting a result")
	insert("preflight", "s6", "failed", "", "reviewer preflight: claude not found")
	insert("other-failure", "s7", "failed", "", "launch reviewer: boom")
	insert("user-cancel", "s8", "cancelled", "", "cancelled by user")
	insert("lifecycle-cancel", "s9", "cancelled", "", "cancelled by worker session lifecycle")
	insert("running", "s10", "running", "", "")

	upTo(t, db, 20261114080351154)

	want := map[string]struct{ status, reason, cancelledBy, body, launchID string }{
		"complete":         {"completed", "", "", "looks good", ""},
		"delivered":        {"completed", "", "", "fix it", ""},
		"superseded":       {"superseded", "", "", "", ""},
		"exited":           {"failed", "process_exited", "", "", ""},
		"chat-turn":        {"failed", "turn_ended_without_submit", "", "", ""},
		"preflight":        {"failed", "preflight_failed", "", "", ""},
		"other-failure":    {"failed", "launch_failed", "", "", ""},
		"user-cancel":      {"cancelled", "", "user", "", ""},
		"lifecycle-cancel": {"cancelled", "", "worker_lifecycle", "", ""},
		"running":          {"running", "", "", "", "launch-7"},
	}
	for id, w := range want {
		var status, reason, cancelledBy, body, launchID string
		var startedAt sql.NullTime
		if err := db.QueryRow(`SELECT status, failure_reason, cancelled_by, body, launch_id, started_at FROM review_run WHERE id = ?`, id).
			Scan(&status, &reason, &cancelledBy, &body, &launchID, &startedAt); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if status != w.status || reason != w.reason || cancelledBy != w.cancelledBy || body != w.body || launchID != w.launchID {
			t.Errorf("%s = {%s %s %s %q %s}, want %+v", id, status, reason, cancelledBy, body, launchID, w)
		}
		if id == "running" && !startedAt.Valid {
			t.Errorf("running row has no started_at")
		}
	}

	// The lock now covers every active state: a queued pass blocks another
	// active pass on the same head and harness, a final one does not.
	insert("queued", "s11", "queued", "", "")
	if _, err := db.Exec(`
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at)
VALUES ('dup', 'rev-1', 'w-1', 'claude-code', 'https://github.com/o/r/pull/1', 's11', 'launching', '', '', ?);`, at); err == nil {
		t.Fatal("second active run on the same head and harness was accepted")
	}
	insert("after-final", "s1", "queued", "", "")
}
