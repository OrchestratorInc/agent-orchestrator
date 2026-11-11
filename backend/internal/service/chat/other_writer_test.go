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

func TestSendDuringTheStopForAnotherProcessDoesNotWakeTheChat(t *testing.T) {
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
	stopping, release := make(chan struct{}), make(chan struct{})
	old.started, old.release = stopping, release

	first := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "elsewhere-5"})
		first <- err
	}()
	select {
	case <-stopping:
	case <-time.After(2 * time.Second):
		t.Fatal("the first send never stopped the provider")
	}
	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "second", ClientMessageID: "elsewhere-6"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send during the stop = %+v, %v; want it queued", turn, err)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first send: %v", err)
	}
	select {
	case <-woke:
		t.Fatal("a send during the stop woke the chat while another process writes it")
	case <-time.After(200 * time.Millisecond):
	}

	elsewhere.Store(false)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not wake after the other process stopped")
	}
}

// expectHeldUntilWriterLeaves checks a queued message waits, with the chat
// marked open elsewhere, until the other process leaves, then reaches the
// resumed provider.
func expectHeldUntilWriterLeaves(t *testing.T, h *harness, woke <-chan struct{}, elsewhere *atomic.Bool, resumed *fakeConversation, text string) {
	t.Helper()
	ctx := context.Background()
	select {
	case <-woke:
		t.Fatal("the chat woke while another process holds it")
	case <-time.After(200 * time.Millisecond):
	}
	if snapshot, err := h.svc.Snapshot(ctx, testSession); err != nil || !snapshot.OpenElsewhere {
		t.Fatalf("snapshot while held = elsewhere %v, %v", snapshot.OpenElsewhere, err)
	}
	elsewhere.Store(false)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the chat did not wake after the other process left")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 && got[0] == text {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resumed provider received %v", resumed.sentTexts())
}

func TestSendToASleepingChatWaitsWhileAnotherProcessHoldsIt(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return true }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || !stopped {
		t.Fatalf("idle hibernation = %v, %v", stopped, err)
	}
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	woke := wakeOnNewRelease(t, h, old, nil)

	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while asleep", ClientMessageID: "elsewhere-7"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued", turn, err)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "while asleep")
}

func TestSendThatSwitchesReleaseWaitsWhileAnotherProcessHoldsIt(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	h, old, resumed := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	woke := wakeOnNewRelease(t, h, old, nil)

	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after update", ClientMessageID: "elsewhere-8"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued", turn, err)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "after update")
}

type failingSessionRead struct {
	*hibernationSessionStore
	failNext atomic.Bool
}

func (s *failingSessionRead) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if s.failNext.CompareAndSwap(true, false) {
		return domain.SessionRecord{}, false, errors.New("database is locked")
	}
	return s.hibernationSessionStore.GetSession(ctx, id)
}

func TestHeldChatSurvivesAFailedSessionRead(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var reads *failingSessionRead
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		reads = &failingSessionRead{hibernationSessionStore: opts.Sessions.(*hibernationSessionStore)}
		opts.Sessions = reads
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	woke := wakeOnNewRelease(t, h, old, nil)
	if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while the terminal is open", ClientMessageID: "elsewhere-9"}); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued", turn, err)
	}

	reads.failNext.Store(true)
	for deadline := time.Now().Add(2 * time.Second); reads.failNext.Load(); {
		if time.Now().After(deadline) {
			t.Fatal("the waiter stopped reading the session")
		}
		time.Sleep(time.Millisecond)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "while the terminal is open")
}

func TestSendSeesTurnsWrittenWhileTheProviderStarted(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	var h *harness
	var old *hibernationConversation
	var terminalTurn atomic.Pointer[time.Time]
	resumes := make(chan *hibernationConversation, 4)
	h, old = hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return true }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) {
			h.advance(time.Second)
			written := h.now()
			terminalTurn.Store(&written)
			h.advance(time.Second)
			conv := &hibernationConversation{fakeConversation: newFakeConversation()}
			conv.turnSeq = 1
			resumes <- conv
			return conv, nil
		}
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || !stopped {
		t.Fatalf("idle hibernation = %v, %v", stopped, err)
	}
	woke := wakeOnNewRelease(t, h, old, nil)
	if err := h.svc.SetChatView(ctx, testSession, "view-1", true); err != nil {
		t.Fatal(err)
	}
	<-woke
	first := <-resumes
	h.svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) bool {
		written := terminalTurn.Load()
		return !since.IsZero() && written != nil && since.Before(*written)
	})

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after the terminal", ClientMessageID: "elsewhere-10"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it held for a reload", turn, err)
	}
	if sent := first.sentTexts(); len(sent) != 0 {
		t.Fatalf("provider started before the terminal's turn received %v", sent)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the chat did not wake to reload the transcript")
	}
	second := <-resumes
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(second.sentTexts()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if sent := second.sentTexts(); len(sent) != 1 || sent[0] != "after the terminal" {
		t.Fatalf("reloaded provider received %v", sent)
	}
}
