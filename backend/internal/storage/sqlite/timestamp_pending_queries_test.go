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
	if _, err := db.Exec(`CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES('2026-10-21 09:00:00 +0530 IST'); CREATE TRIGGER block_background BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'background blocked'); END`); err != nil {
		t.Fatal(err)
	}
	// Public startup must finish the ordering fence before publishing readers.
	s, err := Open(dir)
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

// The newest-reviewer pick sorts review.updated_at as text, so review must be
// normalized before readers open while background repair is still pending.
func TestPendingRepairNewestReview(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const local = "2026-10-21 09:00:00 +0530 IST" // 03:30 UTC
	_, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('p','/tmp/p','2026-10-21 03:00:00 +0000 UTC');
INSERT INTO sessions(id,project_id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
VALUES('s','p',1,'aider','idle','2026-10-21 03:00:00 +0000 UTC',0,'2026-10-21 03:00:00 +0000 UTC','2026-10-21 03:00:00 +0000 UTC');
INSERT INTO review(id,session_id,project_id,harness,created_at,updated_at) VALUES('older','s','p','claude',?,?);
CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES(?);
CREATE TRIGGER block_background BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'background blocked'); END`, local, local, local)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newer := time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC)
	if err := s.UpsertReview(context.Background(), domain.Review{ID: "newer", SessionID: "s", ProjectID: "p", Harness: "codex", CreatedAt: newer, UpdatedAt: newer}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetReviewBySession(context.Background(), "s")
	if err != nil || !ok || got.ID != "newer" {
		t.Fatalf("newest review = %q ok=%v err=%v; want newer", got.ID, ok, err)
	}
}

// A row skipped over a same-instant conflict must be retried once that conflict
// is resolved; otherwise its stale local-time text outranks newer bindings.
func TestSkippedRowRetriedAfterConflictResolved(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const local = "2026-10-21 09:00:00 +0530 IST" // 03:30 UTC
	_, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('p','/tmp/p','2026-10-21 03:00:00 +0000 UTC');
INSERT INTO sessions(id,project_id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
VALUES('s','p',1,'aider','idle','2026-10-21 03:00:00 +0000 UTC',0,'2026-10-21 03:00:00 +0000 UTC','2026-10-21 03:00:00 +0000 UTC');
INSERT INTO session_agent_accounts(session_id,provider,account_id,bound_at) VALUES('s','claude','legacy-account',?),('s','claude','utc-account','2026-10-21 03:30:00 +0000 UTC')`, local)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir) // skips the legacy row: same instant, different accounts
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
	if err := s.BindSessionAgentAccount(ctx, "s", domain.AgentAccountProviderClaude, "legacy-account", at); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(ctx, db); err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM session_agent_accounts WHERE CAST(bound_at AS TEXT) LIKE '%IST'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("resolved conflict left %d legacy rows: %v", legacy, err)
	}
	if err := s.BindSessionAgentAccount(ctx, "s", domain.AgentAccountProviderClaude, "newest", at.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.GetSessionAgentAccount(ctx, "s", domain.AgentAccountProviderClaude); err != nil || !ok || got != "newest" {
		t.Fatalf("account = %q ok=%v err=%v; want newest", got, ok, err)
	}
}
