package chat_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestSendHoldsMessageWhileAnotherProcessWritesThenDeliversToResumedProvider(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	var elsewhere, sawStart atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) bool {
		if !since.IsZero() {
			sawStart.Store(true)
		}
		return elsewhere.Load()
	})
	if err := h.svc.SetChatView(ctx, testSession, "view-1", true); err != nil {
		t.Fatal(err)
	}
	woke := wakeOnNewRelease(t, h, old, nil)

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while the terminal is open", ClientMessageID: "elsewhere-1"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued", turn, err)
	}
	if !sawStart.Load() {
		t.Fatal("send did not pass the provider's start time to the check")
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil || !snapshot.OpenElsewhere || snapshot.Controller != ports.ChatControllerHibernated {
		t.Fatalf("snapshot while held = elsewhere %v, controller %s, %v", snapshot.OpenElsewhere, snapshot.Controller, err)
	}
	_ = h.svc.SetChatView(ctx, testSession, "view-2", true)
	if len(woke) != 0 {
		t.Fatal("a new view woke the session while another process writes it")
	}
	if sent := old.sentTexts(); len(sent) != 1 {
		t.Fatalf("old provider received %v; want only the first message", sent)
	}

	elsewhere.Store(false)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not wake after the other process stopped")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 && got[0] == "while the terminal is open" {
			snapshot, err := h.svc.Snapshot(ctx, testSession)
			if err != nil || snapshot.OpenElsewhere {
				t.Fatalf("snapshot after delivery = elsewhere %v, %v", snapshot.OpenElsewhere, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resumed provider received %v, old %v", resumed.sentTexts(), old.sentTexts())
}

func TestSendKeepsProviderWhenNoOtherProcessWrites(t *testing.T) {
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return false })
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "next", ClientMessageID: "elsewhere-2"}); err != nil {
		t.Fatal(err)
	}
	if conv.calls.Load() != 0 || len(conv.sentTexts()) != 2 {
		t.Fatalf("stops=%d sent=%v; want the running provider to take the message", conv.calls.Load(), conv.sentTexts())
	}
}
