package sqlite

import "testing"

const (
	beforeBrowserApprovalNotifications = 20261121120000000
	browserApprovalNotifications       = 20261123180000000
)

func TestMigrationBrowserApprovalNotificationsRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeBrowserApprovalNotifications)
	const timestamp = "2026-11-23T08:00:00Z"
	if _, err := db.Exec(`
INSERT INTO sessions (id, num, kind, activity_last_at, created_at, updated_at)
VALUES ('standalone-1', 1, 'worker', ?, ?, ?);
INSERT INTO notifications (id, session_id, pr_url, type, title, body, status, created_at, source_key)
VALUES ('notice-1', 'standalone-1', 'https://github.com/acme/app/pull/42', 'review_completed', 'Done', 'Body', 'read', ?, 'review:1');`,
		timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	insertApproval := func() error {
		_, err := db.Exec(`
INSERT INTO notifications (id, session_id, type, title, body, status, created_at, source_key)
VALUES ('approval-1', 'standalone-1', 'browser_approval', 'Approval', 'Body', 'unread', ?, 'browser_approval:apr_1')`, timestamp)
		return err
	}
	if insertApproval() == nil {
		t.Fatal("browser_approval was accepted before the migration")
	}

	upTo(t, db, browserApprovalNotifications)
	if err := insertApproval(); err != nil {
		t.Fatalf("insert browser_approval after migration: %v", err)
	}
	var typ, sourceKey string
	if err := db.QueryRow(`SELECT type, source_key FROM notifications WHERE id = 'notice-1'`).Scan(&typ, &sourceKey); err != nil {
		t.Fatal(err)
	}
	if typ != "review_completed" || sourceKey != "review:1" {
		t.Fatalf("existing notification changed: type=%q source=%q", typ, sourceKey)
	}
	for _, index := range []string{
		"idx_notifications_status_history", "idx_notifications_history", "idx_notifications_unresolved",
		"idx_notifications_live_history", "idx_notifications_open_dedupe", "idx_notifications_source_dedupe",
		"idx_notifications_session",
	} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("index %s missing after migration", index)
		}
	}

	downTo(t, db, beforeBrowserApprovalNotifications)
	var approvals, kept int
	if err := db.QueryRow(`SELECT count(*) FROM notifications WHERE type = 'browser_approval'`).Scan(&approvals); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM notifications WHERE id = 'notice-1' AND source_key = 'review:1'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if approvals != 0 || kept != 1 {
		t.Fatalf("after rollback approvals=%d kept=%d", approvals, kept)
	}
}
