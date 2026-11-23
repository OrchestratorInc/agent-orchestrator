package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Each approval is its own open issue: resolving one leaves the session's
// other waiting approvals open, and the row is listed as unresolved until then.
func TestNotificationStore_ResolveBySourceKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, n := range []struct{ id, key string }{{"ntf_a", "browser_approval:apr_a"}, {"ntf_b", "browser_approval:apr_b"}} {
		rec := domain.NotificationRecord{
			ID: n.id, SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationBrowserApproval,
			Title: "Approval", Status: domain.NotificationUnread, CreatedAt: now, SourceKey: n.key,
		}
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("create %s inserted=%v err=%v", n.id, inserted, err)
		}
	}
	if count, err := s.CountUnresolvedNotifications(ctx); err != nil || count != 2 {
		t.Fatalf("unresolved = %d err=%v, want 2", count, err)
	}
	resolved, err := s.ResolveNotificationBySourceKey(ctx, "browser_approval:apr_a", now)
	if err != nil || len(resolved) != 1 || resolved[0].ID != "ntf_a" || !resolved[0].Resolved() {
		t.Fatalf("resolve = %+v err=%v", resolved, err)
	}
	again, err := s.ResolveNotificationBySourceKey(ctx, "browser_approval:apr_a", now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second resolve = %+v err=%v, want nothing", again, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListUnresolved, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "ntf_b" {
		t.Fatalf("unresolved rows = %+v err=%v", rows, err)
	}
}

// Approvals live in the daemon's memory: none survives a restart, so startup
// reconcile closes every approval row still open.
func TestNotificationStore_ReconcileResolvesOpenBrowserApprovals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.NotificationRecord{
		ID: "ntf_a", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationBrowserApproval,
		Title: "Approval", Status: domain.NotificationUnread, CreatedAt: now, SourceKey: "browser_approval:apr_a",
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("create inserted=%v err=%v", inserted, err)
	}
	resolved, err := s.ReconcileResolvedNotifications(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range resolved {
		found = found || row.ID == "ntf_a"
	}
	if !found {
		t.Fatalf("reconcile resolved %+v, want ntf_a", resolved)
	}
	if count, err := s.CountUnresolvedNotifications(ctx); err != nil || count != 0 {
		t.Fatalf("unresolved = %d err=%v, want 0", count, err)
	}
}
