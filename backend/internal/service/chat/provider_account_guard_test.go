package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestProviderAccountChatPauseFencesSendWithoutInterruptingConversation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	release, err := h.ctrl.AcquireAccountRoutingPause(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.ctrl.AcquireAccountRoutingPause(ctx); !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("second pause=%v", err)
	}
	sent := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after switch", ClientMessageID: "provider-account-message", Origin: domain.MessageOriginHuman})
		sent <- err
	}()
	select {
	case err := <-sent:
		t.Fatalf("send passed account fence: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if got := h.conv.sentTexts(); len(got) != 0 {
		t.Fatalf("provider received prompt while paused=%v", got)
	}
	release()
	release()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("send stayed fenced after account change")
	}
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "after switch" {
		t.Fatalf("provider messages=%v", got)
	}
	if h.hostStops.Load() != 0 {
		t.Fatal("account pause stopped the native provider host")
	}
}
func TestProviderAccountChatPauseRefusesPendingTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", ClientMessageID: "active-account-message", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	release, err := h.ctrl.AcquireAccountRoutingPause(ctx)
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("pending turn pause=%v release=%v", err, release != nil)
	}
	if h.hostStops.Load() != 0 {
		t.Fatal("busy refusal interrupted host")
	}
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "active" {
		t.Fatalf("conversation changed=%v", got)
	}
}
func TestProviderAccountChatPauseRefusesVisibleRunningTurn(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", ClientMessageID: "active-account-message", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["active"] == domain.TurnStateRunning
	})
	release, err := h.ctrl.AcquireAccountRoutingPause(ctx)
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("running turn pause=%v release=%v", err, release != nil)
	}
	if h.hostStops.Load() != 0 {
		t.Fatal("running turn interrupted host")
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["active"] == domain.TurnStateCompleted
	})
	// Persisting the completed message precedes releasing the live turn's
	// admission fence. Wait for that lifecycle boundary, not only the row.
	deadline := time.Now().Add(3 * time.Second)
	for {
		release, err = h.ctrl.AcquireAccountRoutingPause(ctx)
		if err == nil {
			break
		}
		if !errors.Is(err, ports.ErrProviderAccountBusy) || time.Now().After(deadline) {
			t.Fatalf("completed turn remains busy=%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()
}
func TestProviderAccountChatPauseUnknownControllerFailsClosed(t *testing.T) {
	h := newHarness(t)
	release, err := h.svc.AcquireAccountRoutingPause(context.Background(), "unknown")
	if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
		t.Fatalf("unknown controller pause=%v release=%v", err, release != nil)
	}
}
func TestProviderAccountChatPauseCancellationReleasesAdmission(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := h.ctrl.AcquireAccountRoutingPause(ctx)
	if release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled pause=%v release=%v", err, release != nil)
	}
	release, err = h.ctrl.AcquireAccountRoutingPause(context.Background())
	if err != nil {
		t.Fatalf("cancelled pause retained send lock: %v", err)
	}
	release()
}
