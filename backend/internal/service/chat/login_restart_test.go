package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// failNextTurn sends text and has the provider fail it with err.
func failNextTurn(t *testing.T, h *harness, conv *hibernationConversation, text string, err error) string {
	t.Helper()
	turn, sendErr := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: text, ClientMessageID: "failed-" + text})
	if sendErr != nil {
		t.Fatal(sendErr)
	}
	conv.mu.Lock()
	providerTurn := fmt.Sprintf("provider-turn-%d", conv.turnSeq)
	conv.mu.Unlock()
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: providerTurn},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: providerTurn, TurnState: domain.TurnStateFailed, Err: err},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State == domain.TurnStateFailed
			}
		}
		return false
	})
	return turn.ID
}

// refuseNextTurn sends text and has the provider refuse it for its login, as
// Claude Code does when the credential it started with no longer works.
func refuseNextTurn(t *testing.T, h *harness, conv *hibernationConversation, text string) string {
	t.Helper()
	return failNextTurn(t, h, conv, text,
		ports.NewChatProviderFailure("Failed to authenticate. API Error: 401 OAuth access token is invalid.", "", ports.ErrChatAuthRequired))
}

func TestSendingARefusedMessageAgainStartsTheProviderWithTheCurrentLogin(t *testing.T) {
	// Hibernation is off: the restart is for the login, not for idleness.
	h, old, resumed := resumingHarness(t, false)
	refused := refuseNextTurn(t, h, old, "hello")
	resumed.turnSeq = 2
	woke := wakeOnNewRelease(t, h, old, nil)

	if _, err := h.svc.RetryTurn(context.Background(), testSession, refused); err != nil {
		t.Fatalf("retry = %v", err)
	}
	if old.calls.Load() != 1 {
		t.Fatalf("refused provider stopped %d times; want once, before the message went out", old.calls.Load())
	}
	expectWake(t, woke)
	// The provider that refused the login never sees the message again.
	if sent := old.sentTexts(); len(sent) != 2 {
		t.Fatalf("refused provider received %v; want only the first two messages", sent)
	}
	awaitSent(t, resumed, "hello")
}

func TestANewMessageAfterARefusedLoginStartsTheProviderAgain(t *testing.T) {
	h, old, resumed := resumingHarness(t, false)
	refuseNextTurn(t, h, old, "hello")
	resumed.turnSeq = 2
	woke := wakeOnNewRelease(t, h, old, nil)

	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "after the login", ClientMessageID: "after-login"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send = %+v, %v; want it queued for the restarted provider", turn, err)
	}
	if old.calls.Load() != 1 {
		t.Fatalf("refused provider stopped %d times; want once", old.calls.Load())
	}
	expectWake(t, woke)
	awaitSent(t, resumed, "after the login")
}

func TestRetryingAnotherFailureKeepsTheProvider(t *testing.T) {
	h, old, _ := resumingHarness(t, false)
	turn := failNextTurn(t, h, old, "flaky", errors.New("overloaded"))
	woke := wakeOnNewRelease(t, h, old, nil)

	if _, err := h.svc.RetryTurn(context.Background(), testSession, turn); err != nil {
		t.Fatalf("retry = %v", err)
	}
	if old.calls.Load() != 0 {
		t.Fatalf("provider stopped %d times for a failure that was not the login", old.calls.Load())
	}
	expectNoWake(t, woke)
}

func TestARefusedMessageSentAgainWhileStillSignedOutReportsTheLogin(t *testing.T) {
	h, old, _ := resumingHarness(t, false)
	refused := refuseNextTurn(t, h, old, "hello")
	woke := wakeOnNewRelease(t, h, old, ports.ErrAgentAuthRequired)

	_, err := h.svc.RetryTurn(context.Background(), testSession, refused)
	if !errors.Is(err, ports.ErrChatAuthRequired) {
		t.Fatalf("retry = %v; want the login still required", err)
	}
	expectWake(t, woke)
}
