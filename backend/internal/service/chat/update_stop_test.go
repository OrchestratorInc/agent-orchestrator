package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestUpdateStopsAnIdleOrchestrator(t *testing.T) {
	h, conv := settledHibernationHarness(t, domain.TurnStateCompleted)
	ctx := context.Background()
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get orchestrator = %v, %v", found, err)
	}
	rec.Kind = domain.KindOrchestrator
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// Idle hibernation keeps it running; an update that cannot replace a
	// running executable must still be able to stop it.
	stopped, err := h.svc.HibernateChatForUpdate(ctx, testSession)
	if err != nil || !stopped || conv.calls.Load() != 1 {
		t.Fatalf("update stop of an idle orchestrator: stopped=%v err=%v provider stops=%d", stopped, err, conv.calls.Load())
	}
}

func TestMessageSentDuringAnUpdateWaitsForItsResume(t *testing.T) {
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	if stopped, err := h.svc.HibernateChatForUpdate(ctx, testSession); err != nil || !stopped {
		t.Fatalf("update stop = %v, %v", stopped, err)
	}
	// The installer still holds the harness: the message's wake gives up.
	wakeDone := make(chan struct{})
	h.svc.SetWakeCallback(func(context.Context, domain.SessionID) error {
		defer close(wakeDone)
		return errors.New("resume agent mer-1: session: harness install active")
	})
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "sent during update", ClientMessageID: "update-send-1"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v", turn, err)
	}
	select {
	case <-wakeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("background wake did not run")
	}
	time.Sleep(100 * time.Millisecond)
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if last := snapshot.Turns[len(snapshot.Turns)-1]; last.State != domain.TurnStateQueued {
		t.Fatalf("message after the failed wake = %s (%s), want still queued", last.State, last.ErrorMessage)
	}

	// The update finishes and starts the session again, as Session Manager's
	// resume does, then delivers what queued.
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if rec.HibernatedAt != nil {
		if _, err := h.st.SetSessionHibernated(ctx, testSession, rec.Revision, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 && got[0] == "sent during update" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queued message after the update's resume = %v, want delivered once", resumed.sentTexts())
}
