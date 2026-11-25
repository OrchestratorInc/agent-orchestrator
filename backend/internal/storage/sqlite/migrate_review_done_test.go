package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestTimestampRepairPreservesReviewDoneNotices(t *testing.T) {
	db := openMigratedTestDB(t)
	at := time.Now().UTC()
	const legacy = "2026-10-21 09:00:00 +0530 IST"
	const normalized = "2026-10-21 03:30:00 +0000 UTC"
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', ?);`, at)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at, auto_inject_review)
VALUES ('w', 'p', 1, 'codex', ?, ?, ?, TRUE);`, at, at, at)
	mustExec(t, db, `INSERT INTO review (id, session_id, project_id, harness, created_at, updated_at)
VALUES ('r', 'w', 'p', 'codex', ?, ?);`, at, at)
	for _, tc := range []struct{ status, notice string }{
		{"running", ""},
		{"completed", ""},
		{"completed", "pending"},
		{"completed", "sent"},
	} {
		mustExec(t, db, `INSERT INTO review_run (id, review_id, session_id, harness, status, created_at,
worker_notice, worker_notice_attempts, worker_notice_error, worker_notice_attempted_at)
VALUES (?, 'r', 'w', 'codex', ?, ?, ?, 2, 'send failed', ?);`, tc.status+"-"+tc.notice, tc.status, legacy, tc.notice, legacy)
	}
	facts := func() string {
		t.Helper()
		var value string
		if err := db.QueryRow(`SELECT json_array(
(SELECT json_group_array(json_array(id,status,worker_notice,worker_notice_attempts,worker_notice_error)) FROM review_run),
(SELECT count(*) FROM change_log),(SELECT seq FROM sqlite_sequence WHERE name='change_log'))`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := facts()
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if after := facts(); after != before {
		t.Fatalf("timestamp repair changed review notice or change-log facts: %s -> %s", before, after)
	}
	rows, err := db.Query(`SELECT id,CAST(created_at AS TEXT),CAST(worker_notice_attempted_at AS TEXT) FROM review_run`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, created, attempted string
		if err := rows.Scan(&id, &created, &attempted); err != nil {
			t.Fatal(err)
		}
		if created != normalized || attempted != normalized {
			t.Errorf("run %s timestamps = (%q, %q), want (%q, %q)", id, created, attempted, normalized, normalized)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestReviewDoneNoticeQueuesCurrentLifecycleTransitions(t *testing.T) {
	db := openMigratedTestDB(t)
	at := time.Now().UTC()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('p', '/tmp/p', ?);`, at)
	mustExec(t, db, `INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at, auto_inject_review)
VALUES ('w', 'p', 1, 'codex', ?, ?, ?, TRUE);`, at, at, at)
	mustExec(t, db, `INSERT INTO review (id, session_id, project_id, harness, created_at, updated_at)
VALUES ('r', 'w', 'p', 'codex', ?, ?);`, at, at)

	for _, reason := range []string{"preflight_failed", "launch_failed"} {
		mustExec(t, db, `INSERT INTO review_run (id, review_id, session_id, harness, status, created_at, trigger_source, requester_session_id)
 VALUES (?, 'r','w','codex','launching',?,'agent','w')`, reason, at)
		mustExec(t, db, `UPDATE review_run SET status='failed',failure_reason=? WHERE id=?`, reason, reason)
		var notice string
		if err := db.QueryRow(`SELECT worker_notice FROM review_run WHERE id=?`, reason).Scan(&notice); err != nil || notice != "" {
			t.Fatalf("synchronous %s: notice=%q error=%v", reason, notice, err)
		}
	}

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
			mustExec(t, db, `INSERT INTO review_run (id, review_id, session_id, harness, status, created_at, trigger_source, requester_session_id)
VALUES (?, 'r', 'w', 'codex', ?, ?, 'agent', 'w');`, id, tc.from, at)
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
