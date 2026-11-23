package push

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/approval"
	"github.com/aoagents/agent-orchestrator/backend/internal/approvalnotify"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/notify"
)

// A summary can carry what the agent typed (a fallback for an unreadable
// page). The notification is built from the category and site alone.
func TestApprovalNotificationNeverCarriesTheSummary(t *testing.T) {
	gate, err := approval.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &recordingStore{}
	mgr := notify.New(notify.Deps{Store: store})
	n := approvalnotify.New(mgr, func(string) (string, domain.ProjectID) { return "Checkout flow", "" }, nil)
	gate.Observe(n.Observe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)

	const secret = "hunter2-Sup3rSecret"
	go func() {
		_, _ = gate.Request(context.Background(), approval.Request{
			Kind: approval.KindBrowserAction, SessionID: "sess-1", Site: "https://mail.example",
			Action: `Run "type @e3 ` + secret + `\n" on mail.example`, Category: approval.CategoryScript,
			Reason: "typed " + secret, Element: `textbox "Password ` + secret + `"`, Timeout: time.Minute,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for store.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	rec := store.first()
	if rec.ID == "" {
		t.Fatal("no notification written")
	}
	if strings.Contains(rec.Title+rec.Body, secret) || strings.Contains(rec.Title+rec.Body, "@e3") {
		t.Fatalf("notification carries agent text: %q / %q", rec.Title, rec.Body)
	}
	if want := "Checkout flow wants to run a browser action on mail.example"; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
	msg := messageFor(rec, "tok", "host-1", "Mac")
	raw, _ := json.Marshal(msg)
	if strings.Contains(string(raw), secret) {
		t.Fatalf("push message carries agent text: %s", raw)
	}
}

type recordingStore struct {
	mu   sync.Mutex
	rows []domain.NotificationRecord
}

func (r *recordingStore) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.rows) }
func (r *recordingStore) first() domain.NotificationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.rows) == 0 {
		return domain.NotificationRecord{}
	}
	return r.rows[0]
}
func (r *recordingStore) CreateNotification(_ context.Context, rec domain.NotificationRecord) (domain.NotificationRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, rec)
	return rec, true, nil
}
func (r *recordingStore) ResolveSessionNotifications(context.Context, domain.SessionID, domain.NotificationType, time.Time) ([]domain.NotificationRecord, error) {
	return nil, nil
}
func (r *recordingStore) ResolvePRNotifications(context.Context, string, domain.NotificationType, time.Time) ([]domain.NotificationRecord, error) {
	return nil, nil
}
func (r *recordingStore) ReconcileResolvedNotifications(context.Context, time.Time) ([]domain.NotificationRecord, error) {
	return nil, nil
}
func (r *recordingStore) ResolveNotificationBySourceKey(context.Context, string, time.Time) ([]domain.NotificationRecord, error) {
	return nil, nil
}
