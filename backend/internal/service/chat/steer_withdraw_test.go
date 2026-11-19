package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// waitingSteerer is a provider whose Steer waits for the agent to collect the
// guidance, the way a drain agent such as Qwen Code does, until it is withdrawn.
type waitingSteerer struct {
	*steerRecorder
	parked    chan struct{}
	withdrawn chan struct{}
}

func (w *waitingSteerer) Steer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
) (ports.ChatTurnRef, error) {
	if _, err := w.steerRecorder.Steer(ctx, providerTurnID, msg); err != nil {
		return ports.ChatTurnRef{}, err
	}
	close(w.parked)
	select {
	case <-w.withdrawn:
		return ports.ChatTurnRef{}, ports.ErrChatTurnNotSteerable
	case <-time.After(time.Minute):
		return ports.ChatTurnRef{ProviderTurnID: providerTurnID}, nil
	}
}

func (w *waitingSteerer) WithdrawPendingSteers() func() {
	select {
	case <-w.withdrawn:
	default:
		close(w.withdrawn)
	}
	return nil
}

// A steer waiting on the agent holds the dispatch lock. Stop must withdraw it
// rather than wait for it to time out.
func TestInterruptWithdrawsASteerWaitingForTheAgent(t *testing.T) {
	provider := &waitingSteerer{
		steerRecorder: newSteerRecorder(),
		parked:        make(chan struct{}),
		withdrawn:     make(chan struct{}),
	}
	h := newHarnessWithConversationAndStore(t, provider, func(st *store.Store) chatsvc.Store { return st })
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	steered := make(chan error, 1)
	go func() {
		_, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
			Text: "use staging instead", ClientMessageID: "steer-1", Origin: domain.MessageOriginHuman,
		})
		steered <- err
	}()
	select {
	case <-provider.parked:
	case <-time.After(5 * time.Second):
		t.Fatal("steer never reached the provider")
	}

	interrupted := make(chan error, 1)
	go func() { interrupted <- h.svc.Interrupt(ctx, testSession) }()
	select {
	case err := <-interrupted:
		if err != nil {
			t.Fatalf("Interrupt: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited behind the steer")
	}
	if err := <-steered; !errors.Is(err, chatsvc.ErrTurnNotSteerable) {
		t.Fatalf("Steer error = %v, want ErrTurnNotSteerable", err)
	}
}

// failingStopSteerer refuses to interrupt, as a provider whose cancel did not
// get through does, and records whether Stop gave steering back.
type failingStopSteerer struct {
	*waitingSteerer
	restored chan struct{}
}

func (f *failingStopSteerer) Interrupt(context.Context, string) error {
	return errors.New("session/cancel did not reach the agent")
}

func (f *failingStopSteerer) WithdrawPendingSteers() func() {
	f.waitingSteerer.WithdrawPendingSteers()
	return func() {
		select {
		case <-f.restored:
		default:
			close(f.restored)
		}
	}
}

// Stop withdraws the turn's steers before it interrupts. When the interrupt
// fails the turn keeps running, so Stop gives steering back.
func TestInterruptThatFailsRestoresSteering(t *testing.T) {
	provider := &failingStopSteerer{
		waitingSteerer: &waitingSteerer{
			steerRecorder: newSteerRecorder(),
			parked:        make(chan struct{}),
			withdrawn:     make(chan struct{}),
		},
		restored: make(chan struct{}),
	}
	h := newHarnessWithConversationAndStore(t, provider, func(st *store.Store) chatsvc.Store { return st })
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})

	if err := h.svc.Interrupt(ctx, testSession); err == nil {
		t.Fatal("Interrupt succeeded although the provider refused it")
	}
	select {
	case <-provider.withdrawn:
	default:
		t.Fatal("Stop did not withdraw the turn's steers first")
	}
	select {
	case <-provider.restored:
	default:
		t.Fatal("Stop that failed did not give steering back")
	}
}
