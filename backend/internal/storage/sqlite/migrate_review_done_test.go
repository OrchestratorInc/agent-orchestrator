package sqlite

import (
	"testing"
	"time"
)

func TestReviewDoneNoticeQueuesCurrentLifecycleTransitions(t *testing.T) {
	db := openMigratedTestDB(t)
	at := time.Now().UTC()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', ?);`, at)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at, auto_inject_review)
VALUES ('w', 'p', 1, 'codex', ?, ?, ?, TRUE);`, at, at, at)
	mustExec(t, db, `INSERT INTO review (id, session_id, project_id, harness, created_at, updated_at)
VALUES ('r', 'w', 'p', 'codex', ?, ?);`, at, at)

	for _, tc := range []struct {
		from, to, want string
	}{
		{"queued", "failed", "pending"},
		{"launching", "failed", "pending"},
		{"launching", "completed", "pending"},
		{"running", "completed", "pending"},
		{"running", "failed", "pending"},
		{"queued", "cancelled", "pending"},
		{"cancelling", "cancelled", "pending"},
		{"running", "superseded", "pending"},
		{"queued", "launching", ""},
		{"running", "cancelling", ""},
		{"completed", "completed", ""},
	} {
		t.Run(tc.from+"_to_"+tc.to, func(t *testing.T) {
			id := tc.from + "-" + tc.to
			mustExec(t, db, `INSERT INTO review_run (id, review_id, session_id, harness, status, created_at)
VALUES (?, 'r', 'w', 'codex', ?, ?);`, id, tc.from, at)
			mustExec(t, db, `UPDATE review_run SET status = ? WHERE id = ?;`, tc.to, id)
			var notice string
			if err := db.QueryRow(`SELECT worker_notice FROM review_run WHERE id = ?;`, id).Scan(&notice); err != nil {
				t.Fatal(err)
			}
			if notice != tc.want {
				t.Fatalf("notice = %q, want %q", notice, tc.want)
			}
		})
	}

	for _, policy := range []string{"auto_inject_review = FALSE", "is_terminated = TRUE"} {
		t.Run(policy, func(t *testing.T) {
			mustExec(t, db, `UPDATE sessions SET auto_inject_review = TRUE, is_terminated = FALSE WHERE id = 'w';`)
			mustExec(t, db, `UPDATE sessions SET `+policy+` WHERE id = 'w';`)
			mustExec(t, db, `INSERT INTO review_run (id, review_id, session_id, harness, status, created_at)
VALUES (?, 'r', 'w', 'codex', 'launching', ?);`, policy, at)
			mustExec(t, db, `UPDATE review_run SET status = 'completed' WHERE id = ?;`, policy)
			var notice string
			if err := db.QueryRow(`SELECT worker_notice FROM review_run WHERE id = ?;`, policy).Scan(&notice); err != nil {
				t.Fatal(err)
			}
			if notice != "" {
				t.Fatalf("notice = %q with delivery disabled", notice)
			}
		})
	}
}
