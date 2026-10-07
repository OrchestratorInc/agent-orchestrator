package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestChatLauncherForwardsSessionRelay(t *testing.T) {
	// Exercise the production wrapper through the required launcher interface,
	// rather than a fake that accidentally supplies extra methods.
	store := sqlitetest.MustOpen(t)
	if _, err := store.CreateSession(context.Background(), domain.SessionRecord{
		ID: "worker-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
	}); err != nil {
		t.Fatal(err)
	}
	var launcher sessionmanager.ChatLauncher = chatLauncher{svc: chatsvc.New(chatsvc.Options{Sessions: store})}
	for _, tc := range []struct {
		name, clientID string
		options        ports.MessageDeliveryOptions
	}{
		{name: "session-origin send", options: ports.MessageDeliveryOptions{SenderSessionID: "orchestrator-1"}},
		{name: "user-authored outbox replay", clientID: "queued-message-1", options: ports.MessageDeliveryOptions{AuthoredByUser: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := launcher.RelaySessionChatTurn(context.Background(), "worker-1", "direction", tc.clientID, tc.options)
			// No controller was started: reaching this service error proves forwarding
			// across the real wrapper instead of failing with session relay unavailable.
			if id != "" || !errors.Is(err, chatsvc.ErrNoController) {
				t.Fatalf("relay = (%q, %v), want chat service's missing-controller error", id, err)
			}
		})
	}
}
