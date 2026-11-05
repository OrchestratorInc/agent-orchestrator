package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
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

// storeActivity records signals and writes their state to the session row, the
// part of the lifecycle reducer hibernation eligibility reads.
type storeActivity struct {
	recordingActivity
	st *sqlite.Store
}

func (a *storeActivity) ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	_ = a.recordingActivity.ApplyActivitySignal(ctx, id, signal)
	rec, found, err := a.st.GetSession(ctx, id)
	if err != nil || !found {
		return err
	}
	rec.Activity = domain.Activity{State: signal.State, LastActivityAt: signal.Timestamp}
	return a.st.UpdateSession(ctx, rec)
}

// A provider process its login refused can hold credentials another process has
// since replaced, and keep refusing every turn while fresh processes on the same
// login work (issue #7677). Once a reading confirms the login, the refused
// process stops, even for a viewed orchestrator, and the next message resumes
// the conversation in a new one. The failed turn is not retried.
func TestConfirmedLoginReplacesRefusedProvider(t *testing.T) {
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var activity *storeActivity
	h, old := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		activity = &storeActivity{st: opts.Store.(*sqlite.Store)}
		opts.Activity = activity
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	h.activity = &activity.recordingActivity
	ctx := context.Background()
	makeOrchestrator(t, h)
	if err := h.svc.SetChatView(ctx, testSession, "view-1", true); err != nil {
		t.Fatal(err)
	}
	refuseLogin(t, h)
	h.advance(time.Second)
	if !confirmLogin(t, h) {
		t.Fatal("ConfirmConversationLogin did not clear the demand")
	}

	h.svc.LoginConfirmed(ctx, testSession)

	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if old.calls.Load() != 1 || rec.HibernatedAt == nil {
		t.Fatalf("refused provider stops = %d, hibernated at = %v; want it stopped", old.calls.Load(), rec.HibernatedAt)
	}
	if rec.Activity.State != domain.ActivityIdle {
		t.Fatalf("activity = %q, want idle", rec.Activity.State)
	}

	woke := wakeOnNewRelease(t, h, old, nil)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after sign-in", ClientMessageID: "after-sign-in"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not wake in a new provider process")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 && got[0] == "after sign-in" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("new provider received %v, refused one %v", resumed.sentTexts(), old.sentTexts())
}

// heldQueueAfterConfirmedLogin leaves "refused" failed on its login with "held"
// queued behind it, the login confirmed, and the refused provider stopped.
func heldQueueAfterConfirmedLogin(t *testing.T) (*harness, *hibernationConversation, *fakeConversation, domain.ConversationTurn) {
	t.Helper()
	resumed := newFakeConversation()
	resumed.turnSeq = 1
	var activity *storeActivity
	h, old := hibernationHarnessWith(t, "", func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return false }
		activity = &storeActivity{st: opts.Store.(*sqlite.Store)}
		opts.Activity = activity
		driver := opts.Drivers.(fakeRegistry).driver.(fakeDriver)
		driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return resumed, nil }
		opts.Drivers = fakeRegistry{driver: driver}
	})
	h.activity = &activity.recordingActivity
	ctx := context.Background()

	refused, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "refused", ClientMessageID: "refused", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: refused.ProviderTurnID})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["refused"] == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "held", ClientMessageID: "held", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: refused.ProviderTurnID,
		TurnState: domain.TurnStateFailed,
		Err:       ports.NewChatProviderFailure("login refused", "", ports.ErrChatAuthRequired),
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["refused"] == domain.TurnStateFailed &&
			s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	// The durable receipt precedes the projector releasing its dispatch lock.
	if err := waitForActivityState(ctx, h, domain.ActivityWaitingInput); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Second)
	if !confirmLogin(t, h) {
		t.Fatal("ConfirmConversationLogin did not clear the demand")
	}

	h.svc.LoginConfirmed(ctx, testSession)

	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if old.calls.Load() != 1 || rec.HibernatedAt == nil {
		t.Fatalf("refused provider stops = %d, hibernated at = %v; a held message must not keep it running", old.calls.Load(), rec.HibernatedAt)
	}
	assertTurnStaysQueued(t, h, "held")
	return h, old, resumed, refused
}

// retrySucceedsThenReleases retries the refused turn on the new provider and
// checks what was queued waits for that retry, then reaches the same provider.
func retrySucceedsThenReleases(t *testing.T, h *harness, resumed *fakeConversation, retry domain.ConversationTurn, queued ...string) {
	t.Helper()
	if got := resumed.sentTexts(); len(got) != 1 || got[0] != "refused" {
		t.Fatalf("new provider received %v, want only the retried message", got)
	}
	for _, text := range queued {
		assertTurnStaysQueued(t, h, text)
	}
	resumed.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: retry.ProviderTurnID},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: retry.ProviderTurnID, TurnState: domain.TurnStateCompleted},
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		delivered := strings.Join(resumed.sentTexts()[1:], "\n")
		missing := false
		for _, text := range queued {
			missing = missing || !strings.Contains(delivered, text)
		}
		if !missing {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queued messages %v did not follow the successful retry: new provider received %v", queued, resumed.sentTexts())
}

// A message queued behind a refused turn stays held, and used to veto the stop:
// Retry then went back to the refused process, and every later refusal hit the
// same veto. The stop now keeps the held message queued, so an explicit Retry
// reaches a new process, and the held message follows only once that succeeds.
func TestConfirmedLoginReplacesRefusedProviderKeepingHeldQueue(t *testing.T) {
	h, old, resumed, refused := heldQueueAfterConfirmedLogin(t)
	ctx := context.Background()

	woke := wakeOnNewRelease(t, h, old, nil)
	retry, err := h.svc.RetryTurn(ctx, testSession, refused.ID)
	if err != nil {
		t.Fatalf("RetryTurn: %v", err)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("Retry did not wake a new provider process")
	}
	if got := old.sentTexts(); len(got) != 1 {
		t.Fatalf("refused provider received %v after it was replaced", got)
	}
	retrySucceedsThenReleases(t, h, resumed, retry, "held")
}

// A wake after the stop can fail, for example on a provider that will not start.
// Session Manager then clears the sleep marker and leaves the agent stopped for
// the existing Resume action. That start must keep what was queued instead of
// settling it as orphaned work: none of it ever reached a provider.
func TestConfirmedLoginKeepsHeldQueueThroughFailedWake(t *testing.T) {
	failWake := func(t *testing.T, h *harness, wakeDone chan<- struct{}) {
		t.Helper()
		h.svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
			defer close(wakeDone)
			rec, _, err := h.st.GetSession(ctx, id)
			if err != nil {
				return err
			}
			if _, err := h.st.SetSessionHibernated(ctx, id, rec.Revision, nil); err != nil {
				return err
			}
			return errors.New("provider would not start")
		})
	}
	resume := func(t *testing.T, h *harness, old *hibernationConversation) {
		t.Helper()
		if _, err := h.svc.Start(context.Background(), chatsvc.StartConfig{
			SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID(),
		}); err != nil {
			t.Fatalf("resume: %v", err)
		}
	}
	// The failed wake must leave the Resume action reachable.
	assertStopped := func(t *testing.T, h *harness) {
		t.Helper()
		snapshot, err := h.svc.Snapshot(context.Background(), testSession)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Controller != ports.ChatControllerStopped {
			t.Fatalf("controller after the failed wake = %q, want stopped", snapshot.Controller)
		}
	}

	t.Run("a new message's wake fails", func(t *testing.T) {
		h, old, resumed, refused := heldQueueAfterConfirmedLogin(t)
		ctx := context.Background()
		wakeDone := make(chan struct{})
		failWake(t, h, wakeDone)
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: "next", ClientMessageID: "next", Origin: domain.MessageOriginHuman,
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wakeDone:
		case <-time.After(2 * time.Second):
			t.Fatal("background wake did not run")
		}
		assertTurnStaysQueued(t, h, "held")
		assertTurnStaysQueued(t, h, "next")
		assertStopped(t, h)

		resume(t, h, old)
		assertTurnStaysQueued(t, h, "held")
		assertTurnStaysQueued(t, h, "next")
		retry, err := h.svc.RetryTurn(ctx, testSession, refused.ID)
		if err != nil {
			t.Fatalf("RetryTurn: %v", err)
		}
		retrySucceedsThenReleases(t, h, resumed, retry, "held", "next")
	})

	t.Run("an explicit retry's wake fails", func(t *testing.T) {
		h, old, resumed, refused := heldQueueAfterConfirmedLogin(t)
		ctx := context.Background()
		failWake(t, h, make(chan struct{}))
		if _, err := h.svc.RetryTurn(ctx, testSession, refused.ID); err == nil {
			t.Fatal("RetryTurn succeeded with a provider that would not start")
		}
		assertTurnStaysQueued(t, h, "held")
		assertStopped(t, h)

		resume(t, h, old)
		assertTurnStaysQueued(t, h, "held")
		retry, err := h.svc.RetryTurn(ctx, testSession, refused.ID)
		if err != nil {
			t.Fatalf("RetryTurn: %v", err)
		}
		retrySucceedsThenReleases(t, h, resumed, retry, "held")
	})
}

func waitForActivityState(ctx context.Context, h *harness, want domain.ActivityState) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, _, err := h.st.GetSession(ctx, testSession)
		if err != nil {
			return err
		}
		if rec.Activity.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("activity = %q, want %q", rec.Activity.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
