package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// wakingLauncher relays like the production chat service: a send to a
// hibernated session wakes it first, through the Manager's wake path.
type wakingLauncher struct {
	*recordingLauncher
	mgr *Manager
}

func (l *wakingLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	if err := l.mgr.WakeHibernatedChat(ctx, id); err != nil {
		return "", err
	}
	return l.recordingLauncher.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

// guardMessenger is the lifecycle messenger: it hands the guard's message to the
// Manager, as the daemon wires it.
type guardMessenger struct{ mgr *Manager }

func (g guardMessenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	return g.mgr.Send(ctx, id, message, nil)
}

// A lifecycle notification reaches a hibernated chat worker through the guard,
// which holds the session's pane-input lease across the send. Waking the worker
// needs an exclusive resume operation that waits for every input lease to drain,
// so the send must not hold that lease while it wakes the session.
func TestGuardSendWakesHibernatedChatWithoutDeadlockingOnItsOwnLease(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(nil)
	mgr.chat = &wakingLauncher{recordingLauncher: launcher, mgr: mgr}
	seedChatResumeSession(store, domain.ActivityIdle)
	rec := store.sessions["mer-1"]
	at := time.Now().UTC().Add(-time.Hour)
	rec.HibernatedAt = &at
	store.sessions[rec.ID] = rec

	guard := sessionguard.New(store, guardMessenger{mgr: mgr}, nil)
	guard.SetInputLease(mgr)

	// Far shorter than the 60s resume window: a send that is not deadlocked
	// finishes in milliseconds.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := guard.Send(ctx, rec.ID, "new review comments on your PR")
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("send to hibernated chat worker deadlocked on its own input lease: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.relayed) != 1 {
		t.Fatalf("relayed = %v, want the notification delivered once after the wake", launcher.relayed)
	}
}
