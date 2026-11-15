package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPendingRepairTimeSelection(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const local = "2026-10-21 09:00:00.123456789 +0530 IST"
	const utc = "2026-10-21 04:00:00.123456789 +0000 UTC"
	_, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('pending','/tmp/pending',?);
INSERT INTO sessions(id,project_id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
VALUES('pending','pending',1,'aider','idle',?,0,?,?);`, utc, utc, utc)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, at string }{{"earlier-local", local}, {"later-utc", utc}} {
		if _, err := db.Exec(`INSERT INTO reports(id,session_id,project_id,created_at,available_at) VALUES(?,'pending','pending',?,?)`, row.id, row.at, row.at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO notifications(id,session_id,project_id,type,title,pr_url,created_at) VALUES(?,'pending','pending','needs_input',?,?,?)`, row.id, row.id, "https://example.test/"+row.id, row.at); err != nil {
			t.Fatal(err)
		}
	}
	// Leave the legacy bytes in place, as they are while background repair is pending.
	s, err := OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 4, 30, 0, 0, time.UTC)
	t.Run("due reports", func(t *testing.T) {
		reports, err := s.ListPendingReports(ctx, at, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(reports) != 2 || reports[0].ID != "earlier-local" {
			t.Fatalf("due reports = %+v; want both reports, earlier-local first", reports)
		}
	})
	t.Run("notification order and cursor", func(t *testing.T) {
		page, err := s.ListNotifications(ctx, domain.NotificationListStatus("all"), time.Time{}, "", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 || page[0].ID != "later-utc" {
			t.Fatalf("latest notification = %+v; want later-utc", page)
		}
		page, err = s.ListNotifications(ctx, domain.NotificationListStatus("all"), page[0].CreatedAt, page[0].ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 1 || page[0].ID != "earlier-local" {
			t.Fatalf("older notification = %+v; want earlier-local", page)
		}
	})
}
