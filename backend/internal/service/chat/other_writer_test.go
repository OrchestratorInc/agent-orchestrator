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
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
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

func TestFailedStopKeepsTheHoldOfAConcurrentStop(t *testing.T) {
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
	// Both sends pass detection and set the hold; each then pauses in the
	// provider's background-work check until the test lets it go.
	var checks atomic.Int32
	paused := []chan struct{}{make(chan struct{}), make(chan struct{})}
	resume := []chan struct{}{make(chan struct{}), make(chan struct{})}
	old.onBackgroundCheck = func(context.Context) {
		if n := checks.Add(1); n <= 2 {
			close(paused[n-1])
			<-resume[n-1]
		}
	}

	aErr, bErr := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "from a", ClientMessageID: "elsewhere-11"})
		aErr <- err
	}()
	<-paused[0]
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "from b", ClientMessageID: "elsewhere-12"})
		bErr <- err
	}()
	<-paused[1]
	close(resume[0])
	if err := <-aErr; !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
		t.Fatalf("send whose stop lost to the other = %v; want ErrChatOpenElsewhere", err)
	}
	if snapshot, err := h.svc.Snapshot(ctx, testSession); err != nil || !snapshot.OpenElsewhere {
		t.Fatalf("snapshot while the other stop runs = elsewhere %v, %v", snapshot.OpenElsewhere, err)
	}
	close(resume[1])
	if err := <-bErr; err != nil {
		t.Fatalf("send that stopped the provider: %v", err)
	}
	if old.calls.Load() != 1 {
		t.Fatalf("provider stops = %d; want 1", old.calls.Load())
	}
	_ = h.svc.SetChatView(ctx, testSession, "view-1", true)
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "from b")
}

func TestRelayHoldsMessageWhileAnotherProcessWrites(t *testing.T) {
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

	if _, err := h.svc.RelayChatTurnWithID(ctx, testSession, "relayed", "elsewhere-13"); err != nil {
		t.Fatalf("relay: %v", err)
	}
	if sent := old.sentTexts(); len(sent) != 1 {
		t.Fatalf("running provider received %v; want only the first message", sent)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "relayed")
}

func TestDeliveryWhileTheAgentWorksIsRefusedWhenAnotherProcessWrites(t *testing.T) {
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	var elsewhere atomic.Bool
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "long task", ClientMessageID: "elsewhere-14"}); err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2"})
	deadline := time.Now().Add(2 * time.Second)
	for h.ctrl.State() != ports.ChatControllerBusy {
		if time.Now().After(deadline) {
			t.Fatalf("controller state = %s; want busy", h.ctrl.State())
		}
		time.Sleep(5 * time.Millisecond)
	}
	elsewhere.Store(true)

	deliveries := map[string]func() error{
		"send": func() error {
			_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "typed", ClientMessageID: "elsewhere-15"})
			return err
		},
		"relay": func() error {
			_, err := h.svc.RelayChatTurnWithID(ctx, testSession, "relayed", "elsewhere-16")
			return err
		},
		"steer": func() error {
			_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-17"})
			return err
		},
		"steer or send": func() error {
			_, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steered or sent", ClientMessageID: "elsewhere-18"}, false)
			return err
		},
	}
	for name, deliver := range deliveries {
		if err := deliver(); !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
			t.Errorf("%s error = %v; want ErrChatOpenElsewhere", name, err)
		}
	}

	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[1].State == domain.TurnStateCompleted
	})
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if sent := conv.sentTexts(); len(sent) != 2 || conv.calls.Load() != 0 {
		t.Fatalf("provider received %v after %d stops; want only the first two messages", sent, conv.calls.Load())
	}
}

func TestSteerOrSendHoldsMessageWhileAnotherProcessWrites(t *testing.T) {
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

	if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{ClientMessageID: "elsewhere-19"}, true); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) || old.calls.Load() != 0 {
		t.Fatalf("recovery = %v after %d stops; want an uncertain receipt and no stop", err, old.calls.Load())
	}
	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-20"}, false)
	if err != nil || result.Steered || result.Turn.State != domain.TurnStateQueued {
		t.Fatalf("steer or send = %+v, %v; want it queued", result, err)
	}
	if sent := old.sentTexts(); len(sent) != 1 {
		t.Fatalf("running provider received %v; want only the first message", sent)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "steered")
}

func TestSteerToAnIdleChatYieldsItToAnotherProcess(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	woke := wakeOnNewRelease(t, h, old, nil)

	if _, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-21"}); !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
		t.Fatalf("steer error = %v; want ErrChatOpenElsewhere", err)
	}
	if snapshot, err := h.svc.Snapshot(ctx, testSession); err != nil || !snapshot.OpenElsewhere {
		t.Fatalf("snapshot after steer = elsewhere %v, %v", snapshot.OpenElsewhere, err)
	}
	elsewhere.Store(false)
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the chat did not wake after the other process left")
	}
	if sent := old.sentTexts(); len(sent) != 1 {
		t.Fatalf("provider received %v; want only the first message", sent)
	}
}

type steerableHibernation struct {
	*hibernationConversation
	steers atomic.Int32
	fail   atomic.Bool
}

func (c *steerableHibernation) Steer(_ context.Context, providerTurnID string, _ ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	c.steers.Add(1)
	if c.fail.Load() {
		return ports.ChatTurnRef{}, errors.New("connection reset while waiting for steer reply")
	}
	return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
}

func TestRetriedSteerReplaysItsReceiptWhileAnotherProcessHoldsTheChat(t *testing.T) {
	t.Cleanup(chatsvc.SetOtherWriterPoll(5 * time.Millisecond))
	var provider *steerableHibernation
	h, _ := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		inner := driver.conv.(*hibernationConversation)
		caps := productionCaps()
		caps[ports.ChatCapabilitySteer] = true
		inner.setCapabilities(caps)
		provider = &steerableHibernation{hibernationConversation: inner}
		driver.conv = provider
		opts.Drivers = fakeRegistry{driver: driver}
	})
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "do work", ClientMessageID: "hibernate-1"}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	accepted := ports.ChatUserMessage{Text: "narrow the search", ClientMessageID: "elsewhere-22"}
	first, err := h.svc.SteerOrSend(ctx, testSession, accepted, false)
	if err != nil || !first.Steered {
		t.Fatalf("first steer = %+v, %v; want it steered", first, err)
	}
	uncertain := ports.ChatUserMessage{Text: "skip the tests", ClientMessageID: "elsewhere-23"}
	provider.fail.Store(true)
	if _, err := h.svc.SteerOrSend(ctx, testSession, uncertain, false); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("second steer error = %v; want ErrSteerDeliveryUncertain", err)
	}
	provider.fail.Store(false)
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if err := h.svc.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	var elsewhere atomic.Bool
	elsewhere.Store(true)
	h.svc.SetOtherWriterCheck(func(context.Context, domain.SessionRecord, time.Time) bool { return elsewhere.Load() })
	t.Cleanup(func() { elsewhere.Store(false) })

	retried, err := h.svc.SteerOrSend(ctx, testSession, accepted, false)
	if err != nil || !retried.Steered || !retried.Duplicate || withoutReplay(retried.Steer) != first.Steer {
		t.Fatalf("retried accepted steer = %+v, %v; want the first result replayed", retried, err)
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil || !snapshot.OpenElsewhere {
		t.Fatalf("snapshot after the retry = elsewhere %v, %v; want the chat held", snapshot.OpenElsewhere, err)
	}
	if replayed, err := h.svc.Steer(ctx, testSession, accepted); err != nil || !replayed.Replayed {
		t.Fatalf("retried plain steer = %+v, %v; want the first result replayed", replayed, err)
	}
	if _, err := h.svc.SteerOrSend(ctx, testSession, uncertain, false); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("retried uncertain steer error = %v; want ErrSteerDeliveryUncertain", err)
	}
	if _, err := h.svc.Steer(ctx, testSession, uncertain); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("retried uncertain plain steer error = %v; want ErrSteerDeliveryUncertain", err)
	}
	if sent, steers := provider.sentTexts(), provider.steers.Load(); len(sent) != 1 || steers != 2 {
		t.Fatalf("provider received sends %v and %d steers; want the first send and two steers", sent, steers)
	}
	if snapshot, err := h.svc.Snapshot(ctx, testSession); err != nil || len(snapshot.Turns) != 1 {
		t.Fatalf("conversation has %d turns, %v; want the retries to add none", len(snapshot.Turns), err)
	}
}

func TestSteerToASleepingChatWaitsWhileAnotherProcessHoldsIt(t *testing.T) {
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

	if _, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-24"}); !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
		t.Fatalf("steer error = %v; want ErrChatOpenElsewhere", err)
	}
	if snapshot, err := h.svc.Snapshot(ctx, testSession); err != nil || snapshot.Controller != ports.ChatControllerHibernated {
		t.Fatalf("controller after steer = %s, %v; want it still asleep", snapshot.Controller, err)
	}
	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steered or sent", ClientMessageID: "elsewhere-25"}, false)
	if err != nil || result.Steered || result.Turn.State != domain.TurnStateQueued {
		t.Fatalf("steer or send = %+v, %v; want it queued", result, err)
	}
	expectHeldUntilWriterLeaves(t, h, woke, &elsewhere, resumed, "steered or sent")
}
