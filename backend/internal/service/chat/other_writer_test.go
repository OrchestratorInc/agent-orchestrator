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

type otherWriterCheck = func(context.Context, domain.SessionRecord, time.Time) (bool, bool)

// otherWriter reports the chat open elsewhere while open is set, and turns
// written by a process that left, once, to a provider started before them.
func otherWriter(open, wrote *atomic.Bool) otherWriterCheck {
	return func(_ context.Context, _ domain.SessionRecord, since time.Time) (bool, bool) {
		if open.Load() {
			return true, false
		}
		return false, !since.IsZero() && wrote.Swap(false)
	}
}

func resumingHarness(t *testing.T, hibernation bool) (*harness, *hibernationConversation, *fakeConversation) {
	t.Helper()
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return hibernation }
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	return h, old, resumed
}

func awaitSent(t *testing.T, conv *fakeConversation, text string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got := conv.sentTexts(); len(got) == 1 && got[0] == text {
			return
		}
	}
	t.Fatalf("provider received %v; want [%q]", conv.sentTexts(), text)
}

func expectWake(t *testing.T, woke <-chan struct{}) {
	t.Helper()
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the chat did not wake")
	}
}

func expectNoWake(t *testing.T, woke <-chan struct{}) {
	t.Helper()
	select {
	case <-woke:
		t.Fatal("the chat woke while another process has it open")
	case <-time.After(100 * time.Millisecond):
	}
}

func expectRefusedUnchanged(t *testing.T, h *harness, err error, controller ports.ChatControllerState, turns int) {
	t.Helper()
	if !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
		t.Fatalf("error = %v; want ErrChatOpenElsewhere", err)
	}
	snapshot, snapErr := h.svc.Snapshot(context.Background(), testSession)
	if snapErr != nil || snapshot.Controller != controller || len(snapshot.Turns) != turns {
		t.Fatalf("after the refusal: controller %s with %d turns, %v; want %s with %d", snapshot.Controller, len(snapshot.Turns), snapErr, controller, turns)
	}
}

func TestSendIsRefusedWhileAnotherProcessHasTheChatOpen(t *testing.T) {
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	var sawStart atomic.Bool
	h.svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) (bool, bool) {
		sawStart.Store(!since.IsZero())
		return true, false
	})
	woke := wakeOnNewRelease(t, h, conv, nil)

	_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "while the terminal is open", ClientMessageID: "elsewhere-1"})
	expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)
	if !sawStart.Load() {
		t.Fatal("send did not pass the provider's start time to the check")
	}
	if sent := conv.sentTexts(); len(sent) != 1 || conv.calls.Load() != 0 {
		t.Fatalf("provider received %v after %d stops; want only the first message and no stop", sent, conv.calls.Load())
	}
	expectNoWake(t, woke)
}

func TestSendReloadsTurnsAnotherProcessWroteBeforeItLeft(t *testing.T) {
	h, old, resumed := resumingHarness(t, false)
	ctx := context.Background()
	var open, wrote atomic.Bool
	wrote.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	woke := wakeOnNewRelease(t, h, old, nil)

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after the terminal", ClientMessageID: "elsewhere-2"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued for the reloaded provider", turn, err)
	}
	if sent := old.sentTexts(); len(sent) != 1 || old.calls.Load() != 1 {
		t.Fatalf("stale provider received %v after %d stops; want it stopped without the message", sent, old.calls.Load())
	}
	expectWake(t, woke)
	awaitSent(t, resumed, "after the terminal")
}

func TestSendKeepsProviderWhenNoOtherProcessWrites(t *testing.T) {
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	var open, wrote atomic.Bool
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "next", ClientMessageID: "elsewhere-3"}); err != nil {
		t.Fatal(err)
	}
	if conv.calls.Load() != 0 || len(conv.sentTexts()) != 2 {
		t.Fatalf("stops=%d sent=%v; want the running provider to take the message", conv.calls.Load(), conv.sentTexts())
	}
}

func TestSendRefusedWhenTheProviderCannotStopToReloadTurns(t *testing.T) {
	for name, refuse := range map[string]func(*hibernationConversation){
		"background work": func(c *hibernationConversation) { c.backgroundRunning = true },
		"check failed":    func(c *hibernationConversation) { c.backgroundErr = errors.New("inventory failed") },
	} {
		t.Run(name, func(t *testing.T) {
			h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
				opts.HibernationEnabled = func() bool { return false }
			})
			refuse(conv)
			var open, wrote atomic.Bool
			wrote.Store(true)
			h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
			_, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "after the terminal", ClientMessageID: "elsewhere-4"})
			expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)
			if sent := conv.sentTexts(); len(sent) != 1 || conv.calls.Load() != 0 {
				t.Fatalf("provider received %v after %d stops; want only the first message", sent, conv.calls.Load())
			}
		})
	}
}

type liveHibernationConversation struct{ *hibernationConversation }

func (*liveHibernationConversation) ReconnectedLive() bool { return true }

func TestSendAfterLiveReconnectSeesTurnsWrittenBeforeIt(t *testing.T) {
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
	svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) (bool, bool) {
		return false, !since.IsZero() && since.Before(terminalTurn)
	})

	turn, err := svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after the restart", ClientMessageID: "elsewhere-5"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued for a reload", turn, err)
	}
	if sent := live.sentTexts(); len(sent) != 0 {
		t.Fatalf("reconnected provider received %v; it never loaded the terminal's turns", sent)
	}
}

func TestSendSeesTurnsWrittenWhileTheProviderStarted(t *testing.T) {
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
	h.svc.SetOtherWriterCheck(func(_ context.Context, _ domain.SessionRecord, since time.Time) (bool, bool) {
		written := terminalTurn.Load()
		return false, !since.IsZero() && written != nil && since.Before(*written)
	})

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after the terminal", ClientMessageID: "elsewhere-6"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued for a reload", turn, err)
	}
	if sent := first.sentTexts(); len(sent) != 0 {
		t.Fatalf("provider started before the terminal's turn received %v", sent)
	}
	expectWake(t, woke)
	awaitSent(t, (<-resumes).fakeConversation, "after the terminal")
}

func TestSendToASleepingChatIsRefusedWhileAnotherProcessHasItOpen(t *testing.T) {
	h, old, resumed := resumingHarness(t, true)
	ctx := context.Background()
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || !stopped {
		t.Fatalf("idle hibernation = %v, %v", stopped, err)
	}
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	woke := wakeOnNewRelease(t, h, old, nil)
	msg := ports.ChatUserMessage{Text: "while asleep", ClientMessageID: "elsewhere-7"}

	_, err := h.svc.Send(ctx, testSession, msg)
	expectRefusedUnchanged(t, h, err, ports.ChatControllerHibernated, 1)
	expectNoWake(t, woke)

	open.Store(false)
	if turn, err := h.svc.Send(ctx, testSession, msg); err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send again after the other process left = %+v, %v; want it queued", turn, err)
	}
	expectWake(t, woke)
	awaitSent(t, resumed, "while asleep")
}

func TestSendThatWouldSwitchReleaseIsRefusedWhileAnotherProcessHasItOpen(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	ctx := context.Background()
	markStale(t, h)
	h.advance(refreshIdle)
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))

	_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after update", ClientMessageID: "elsewhere-8"})
	expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)
	if old.calls.Load() != 0 {
		t.Fatalf("provider stops = %d; want none while another process has the chat open", old.calls.Load())
	}
}

func TestRelayIsRefusedWhileAnotherProcessHasTheChatOpenAndReloadsAfterIt(t *testing.T) {
	h, old, resumed := resumingHarness(t, false)
	ctx := context.Background()
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	woke := wakeOnNewRelease(t, h, old, nil)

	_, err := h.svc.RelayChatTurnWithID(ctx, testSession, "relayed", "elsewhere-9")
	expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)

	open.Store(false)
	wrote.Store(true)
	if _, err := h.svc.RelayChatTurnWithID(ctx, testSession, "relayed", "elsewhere-9"); err != nil {
		t.Fatalf("relay after the other process left: %v", err)
	}
	if sent := old.sentTexts(); len(sent) != 1 || old.calls.Load() != 1 {
		t.Fatalf("stale provider received %v after %d stops; want it stopped without the message", sent, old.calls.Load())
	}
	expectWake(t, woke)
	awaitSent(t, resumed, "relayed")
}

func TestDeliveryWhileTheAgentWorksIsRefusedWhenAnotherProcessWrites(t *testing.T) {
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	var open, wrote atomic.Bool
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "long task", ClientMessageID: "elsewhere-10"}); err != nil {
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

	deliveries := map[string]func() error{
		"send": func() error {
			_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "typed", ClientMessageID: "elsewhere-11"})
			return err
		},
		"relay": func() error {
			_, err := h.svc.RelayChatTurnWithID(ctx, testSession, "relayed", "elsewhere-12")
			return err
		},
		"steer": func() error {
			_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-13"})
			return err
		},
		"steer or send": func() error {
			_, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steered or sent", ClientMessageID: "elsewhere-14"}, false)
			return err
		},
	}
	for state, set := range map[string]func(){
		"open":           func() { open.Store(true) },
		"wrote and left": func() { open.Store(false); wrote.Store(true) },
	} {
		for name, deliver := range deliveries {
			set()
			if err := deliver(); !errors.Is(err, chatsvc.ErrChatOpenElsewhere) {
				t.Errorf("%s while another process %s: error = %v; want ErrChatOpenElsewhere", name, state, err)
			}
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

func TestSteerOrSendIsRefusedWhileAnotherProcessHasTheChatOpenAndReloadsAfterIt(t *testing.T) {
	h, old, resumed := resumingHarness(t, false)
	ctx := context.Background()
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	woke := wakeOnNewRelease(t, h, old, nil)

	if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{ClientMessageID: "elsewhere-15"}, true); !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) || old.calls.Load() != 0 {
		t.Fatalf("recovery = %v after %d stops; want an uncertain receipt and no stop", err, old.calls.Load())
	}
	msg := ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-16"}
	_, err := h.svc.SteerOrSend(ctx, testSession, msg, false)
	expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)

	open.Store(false)
	wrote.Store(true)
	result, err := h.svc.SteerOrSend(ctx, testSession, msg, false)
	if err != nil || result.Steered {
		t.Fatalf("steer or send after the other process left = %+v, %v; want it sent", result, err)
	}
	if sent := old.sentTexts(); len(sent) != 1 || old.calls.Load() != 1 {
		t.Fatalf("stale provider received %v after %d stops; want it stopped without the message", sent, old.calls.Load())
	}
	expectWake(t, woke)
	awaitSent(t, resumed, "steered")
}

func TestSteerToAnIdleChatIsRefusedWhileAnotherProcessHasItOpen(t *testing.T) {
	h, old := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
	})
	ctx := context.Background()
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))

	_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-17"})
	expectRefusedUnchanged(t, h, err, ports.ChatControllerReady, 1)
	if sent := old.sentTexts(); len(sent) != 1 || old.calls.Load() != 0 {
		t.Fatalf("provider received %v after %d stops; want only the first message", sent, old.calls.Load())
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

func TestRetriedSteerReplaysItsReceiptWhileAnotherProcessHasTheChatOpen(t *testing.T) {
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
	accepted := ports.ChatUserMessage{Text: "narrow the search", ClientMessageID: "elsewhere-18"}
	first, err := h.svc.SteerOrSend(ctx, testSession, accepted, false)
	if err != nil || !first.Steered {
		t.Fatalf("first steer = %+v, %v; want it steered", first, err)
	}
	uncertain := ports.ChatUserMessage{Text: "skip the tests", ClientMessageID: "elsewhere-19"}
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
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))

	retried, err := h.svc.SteerOrSend(ctx, testSession, accepted, false)
	if err != nil || !retried.Steered || !retried.Duplicate || withoutReplay(retried.Steer) != first.Steer {
		t.Fatalf("retried accepted steer = %+v, %v; want the first result replayed", retried, err)
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

func TestSteerToASleepingChatIsRefusedWhileAnotherProcessHasItOpen(t *testing.T) {
	h, old, _ := resumingHarness(t, true)
	ctx := context.Background()
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || !stopped {
		t.Fatalf("idle hibernation = %v, %v", stopped, err)
	}
	var open, wrote atomic.Bool
	open.Store(true)
	h.svc.SetOtherWriterCheck(otherWriter(&open, &wrote))
	woke := wakeOnNewRelease(t, h, old, nil)

	_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{Text: "steered", ClientMessageID: "elsewhere-20"})
	expectRefusedUnchanged(t, h, err, ports.ChatControllerHibernated, 1)
	_, err = h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steered or sent", ClientMessageID: "elsewhere-21"}, false)
	expectRefusedUnchanged(t, h, err, ports.ChatControllerHibernated, 1)
	expectNoWake(t, woke)
}
