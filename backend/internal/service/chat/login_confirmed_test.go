package chat_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// refuseLogin runs one turn the provider refuses for its credentials and puts
// the session where the lifecycle reducer leaves it: waiting on the user.
func refuseLogin(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "hello", ClientMessageID: "refused-login", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
			TurnState: domain.TurnStateFailed,
			Err:       ports.NewChatProviderFailure("Not logged in · Please run /login", "", ports.ErrChatAuthRequired),
		},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateFailed &&
			s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityWaitingInput, "chat.account.reauth") {
		t.Fatalf("activity signals = %v, want waiting_input for the refused login", h.activity.snapshot())
	}
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Activity.State = domain.ActivityWaitingInput
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

func confirmLogin(t *testing.T, h *harness) bool {
	t.Helper()
	cleared, err := h.st.ConfirmConversationLogin(context.Background(), h.ctrl.ConversationID(), h.now().Add(1), h.now().Add(1))
	if err != nil {
		t.Fatal(err)
	}
	return cleared
}

// A refused login leaves the session waiting on the user. Once a reading of the
// account finds the login working again and clears the demand, nothing is left
// to wait for, so the chat settles to idle instead of reading as needing input
// until another turn starts (issue #7673).
func TestConfirmedLoginSettlesSessionWaitingOnRefusedLogin(t *testing.T) {
	h := newHarness(t)
	refuseLogin(t, h)
	h.advance(1)
	if !confirmLogin(t, h) {
		t.Fatal("ConfirmConversationLogin did not clear the demand")
	}

	h.svc.LoginConfirmed(context.Background(), testSession)

	if got := countActivitySignals(h.activity.snapshot(), domain.ActivityIdle, "chat.account.reauth_cleared"); got != 1 {
		t.Fatalf("idle reauth_cleared signals = %d, want 1 (signals %v)", got, h.activity.snapshot())
	}
}

func TestConfirmedLoginKeepsSessionWaitingWhileSomethingStillWaits(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, h *harness)
	}{
		{
			// A demand the reading cannot speak for still needs a sign-in.
			name:  "demand still pending",
			setup: func(*testing.T, *harness) {},
		},
		{
			name: "approval pending",
			setup: func(t *testing.T, h *harness) {
				confirmLogin(t, h)
				h.conv.emit(ports.ChatEvent{
					Kind: ports.ChatEventApprovalRequested, ProviderItemID: "approval-1", RequestID: "approval-1",
					ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusPending,
					Summary:   "Run a command",
					Decisions: []ports.ChatDecisionOption{{ID: "accept", Label: "Approve", Kind: ports.ChatDecisionAllowOnce}},
				})
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					for _, activity := range s.Activities {
						if activity.Kind == domain.ActivityKindApproval && activity.Status == domain.ActivityStatusPending {
							return true
						}
					}
					return false
				})
			},
		},
		{
			name: "session no longer waiting",
			setup: func(t *testing.T, h *harness) {
				confirmLogin(t, h)
				ctx := context.Background()
				rec, _, err := h.st.GetSession(ctx, testSession)
				if err != nil {
					t.Fatal(err)
				}
				rec.Activity.State = domain.ActivityActive
				if err := h.st.UpdateSession(ctx, rec); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			refuseLogin(t, h)
			h.advance(1)
			tt.setup(t, h)

			h.svc.LoginConfirmed(context.Background(), testSession)

			if got := countActivitySignals(h.activity.snapshot(), domain.ActivityIdle, "chat.account.reauth_cleared"); got != 0 {
				t.Fatalf("idle reauth_cleared signals = %d, want 0 (signals %v)", got, h.activity.snapshot())
			}
		})
	}
}
