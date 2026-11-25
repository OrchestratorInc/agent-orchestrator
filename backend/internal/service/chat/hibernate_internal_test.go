package chat

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClosedChatViewTimestampExpiresAfterGrace(t *testing.T) {
	now := time.Now()
	svc := New(Options{Now: func() time.Time { return now }})
	id := domain.SessionID("closed-chat")
	svc.setViewLease(id, "view", true)
	svc.setViewLease(id, "view", false)
	if !svc.HasChatView(id) {
		t.Fatal("closing a view skipped the warm grace period")
	}
	now = now.Add(chatHibernateGrace)
	if svc.HasChatView(id) || len(svc.viewClosedAt) != 0 {
		t.Fatalf("expired view retained: %v", svc.viewClosedAt)
	}
}
