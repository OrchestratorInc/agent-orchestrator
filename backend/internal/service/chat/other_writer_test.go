package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

func TestSendRefusedWhenAnotherProcessWritesAndTheProviderCannotStop(t *testing.T) {
	for name, refuse := range map[string]func(*hibernationConversation){
		"background work": func(c *hibernationConversation) { c.backgroundRunning = true },
		"check failed":    func(c *hibernationConversation) { c.backgroundErr = errors.New("inventory failed") },
	} {
		t.Run(name, func(t *testing.T) {
			h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
				opts.HibernationEnabled = func() bool { return false }
			})
			refuse(conv)
			h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return true })
			ctx := context.Background()
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while the terminal is open", ClientMessageID: "elsewhere-3"}); !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
				t.Fatalf("send error = %v; want ErrChatOpenElsewhere", err)
			}
			if sent := conv.sentTexts(); len(sent) != 1 || conv.calls.Load() != 0 {
				t.Fatalf("provider received %v after %d stops; want only the first message", sent, conv.calls.Load())
			}
			snapshot, err := h.svc.Snapshot(ctx, testSession)
			if err != nil || snapshot.OpenElsewhere || snapshot.Controller != ports.ChatControllerReady {
				t.Fatalf("snapshot = elsewhere %v, controller %s, %v", snapshot.OpenElsewhere, snapshot.Controller, err)
			}
		})
	}
}

func TestRetriedSendThatStopsForAnotherProcessWakesOnceItLeaves(t *testing.T) {
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
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	woke := wakeOnNewRelease(t, h, old, nil)

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "do work", ClientMessageID: "hibernate-1"})
	if err != nil || turn.ID != "" || old.calls.Load() != 1 {
		t.Fatalf("retried send = %+v, %v after %d stops; want the duplicate accepted after a stop", turn, err, old.calls.Load())
	}

	elsewhere.Store(false)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("session stayed asleep after the other process stopped")
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil || snapshot.OpenElsewhere {
		t.Fatalf("snapshot after wake = elsewhere %v, %v", snapshot.OpenElsewhere, err)
	}
	if sent := resumed.sentTexts(); len(sent) != 0 {
		t.Fatalf("resumed provider received %v; the retry was already delivered", sent)
	}
}

type liveHibernationConversation struct{ *hibernationConversation }

func (*liveHibernationConversation) ReconnectedLive() bool { return true }

func TestSendAfterLiveReconnectSeesTurnsWrittenBeforeIt(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	h, _ := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	h.svc.StopAll(ctx)
	h.advance(time.Now().Add(time.Hour).Sub(h.now()))
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Kind = domain.KindWorker
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: h.now().Add(-time.Hour)}
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}

	live := &liveHibernationConversation{&hibernationConversation{fakeConversation: newFakeConversation()}}
	live.turnSeq = 1
	var ids atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers:  fakeRegistry{driver: fakeDriver{conv: live}},
		Activity: &recordingActivity{}, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("reconnect-%d", ids.Add(1)) },
	})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: live.ProviderConversationID(),
	}); err != nil {
		t.Fatalf("reconnect start: %v", err)
	}
	terminalTurn := h.now().Add(-time.Minute)
	svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) bool {
		return since.IsZero() || since.Before(terminalTurn)
	})

	turn, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after the restart", ClientMessageID: "elsewhere-4"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it held for a reload", turn, err)
	}
	if sent := live.sentTexts(); len(sent) != 0 {
		t.Fatalf("reconnected provider received %v; it never loaded the terminal's turns", sent)
	}
}
