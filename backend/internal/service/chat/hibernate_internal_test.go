package chat

import (
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestClosedChatViewTimestampExpiresAfterGrace(t *testing.T) {
	now := time.Now()
	svc := New(Options{Now: func() time.Time { return now }})
	id := domain.SessionID("closed-chat")
	svc.setViewLease(id, "view", true)
	svc.setViewLease(id, "view", false)
	if !svc.hasChatView(id) {
		t.Fatal("closing a view skipped the warm grace period")
	}
	now = now.Add(chatHibernateGrace)
	if svc.hasChatView(id) || len(svc.viewClosedAt) != 0 {
		t.Fatalf("expired view retained: %v", svc.viewClosedAt)
	}
}

func TestOpenChatsSkipsClosedViews(t *testing.T) {
	now := time.Now()
	svc := New(Options{Now: func() time.Time { return now }})
	svc.setViewLease("closed", "a", true)
	svc.setViewLease("closed", "a", false)
	svc.setViewLease("expired", "b", true)
	svc.setViewLease("open", "c", true)
	now = now.Add(chatViewLease / 2)
	svc.setViewLease("open", "c", true)
	now = now.Add(chatViewLease / 2)
	if got := svc.OpenChats(); len(got) != 1 || got[0] != "open" {
		t.Fatalf("OpenChats() = %v, want [open]", got)
	}
}
