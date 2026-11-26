package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// assertTurnStaysQueued fails if the named turn leaves queued within a short settle
// window. A drain that should not have run would move it to running almost at once.
func assertTurnStaysQueued(t *testing.T, h *harness, text string) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
		if err != nil {
			t.Fatalf("load snapshot: %v", err)
		}
		if got := turnStateByText(t, snapshot)[text]; got != domain.TurnStateQueued {
			t.Fatalf("queued turn %q became %q; a non-success terminal turn must hold the queue", text, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A failed primary turn must not release queued work into the same outage; only a
// completed turn may. Regression for issue #4861.
func TestFailedPrimaryTurnHoldsQueuedWork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("queue automation: %v", err)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateFailed,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateFailed
	})

	assertTurnStaysQueued(t, h, "automation")
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("provider received %v, want only the root prompt", got)
	}
}

// A recovered turn is terminal but carries no portable outcome, so it is no proof
// the next turn will succeed. It holds the queue like a failed turn.
func TestRecoveredPrimaryTurnHoldsQueuedWork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRunning
	})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("queue automation: %v", err)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateRecovered,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root"] == domain.TurnStateRecovered
	})

	assertTurnStaysQueued(t, h, "automation")
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("provider received %v, want only the root prompt", got)
	}
}

// A prompt the host's watchdog failed for silence never reached an answer, so it
// is no sign the provider is down. Queued work gets its own turn instead of
// waiting for the user to type again. A provider error still holds the queue.
func TestStalledPrimaryTurnReleasesQueuedWork(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		released bool
	}{
		{"watchdog stall releases the queue", fmt.Errorf("ACP session/prompt: %w", ports.ErrChatPromptStalled), true},
		{"provider error holds the queue", errors.New("ACP session/prompt: provider unavailable"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "root", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
			}); err != nil {
				t.Fatalf("send root: %v", err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["root"] == domain.TurnStateRunning
			})
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
			}); err != nil {
				t.Fatalf("queue automation: %v", err)
			}

			h.conv.emit(ports.ChatEvent{
				Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
				TurnState: domain.TurnStateFailed, Err: tc.err,
			})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["root"] == domain.TurnStateFailed
			})

			if !tc.released {
				assertTurnStaysQueued(t, h, "automation")
				return
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["automation"] != domain.TurnStateQueued
			})
			if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "automation" {
				t.Fatalf("provider received %v, want the root prompt then the queued one", got)
			}
		})
	}
}

func TestStalledRetryReleasesTheUsageLimitHoldWithItsFailure(t *testing.T) {
	conv := &receiptConversation{
		terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()},
		ack: func(id string) error {
			if id == "stall" {
				return errors.New("connection lost before ack")
			}
			return nil
		},
	}
	h := newHarnessWithConversation(t, conv)
	h.conv = conv.fakeConversation
	ctx := context.Background()
	source := failUsageLimitedTurnBehindQueue(t, h, "queued note")
	retry, err := h.svc.RetryTurn(ctx, testSession, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: retry.ProviderTurnID, ProviderEventID: "stall",
		TurnState: domain.TurnStateFailed, Err: fmt.Errorf("ACP session/prompt: %w", ports.ErrChatPromptStalled),
	})
	done := make(chan struct{})
	go func() { h.ctrl.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ack failure did not detach")
	}
	if held, err := h.st.ConversationQueueHeld(ctx, h.ctrl.ConversationID(), h.now()); err != nil || held {
		t.Fatalf("queue held = %v, %v after the stalled retry's failure committed", held, err)
	}
}

func TestUsageLimitHoldQueuesAgentSendsOnAnIdleChat(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "finish this task", ClientMessageID: "c-limited", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child-turn", ProviderConversationID: "child-thread"})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-turn", ProviderConversationID: "child-thread", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ProviderTurnID == "child-turn" {
				return turn.State == domain.TurnStateCompleted
			}
		}
		return false
	})
	if held, err := h.st.ConversationQueueHeld(ctx, h.ctrl.ConversationID(), h.now()); err != nil || !held {
		t.Fatalf("queue held = %v, %v after a child thread completed", held, err)
	}

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "agent note", ClientMessageID: "c-agent", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	assertTurnStaysQueued(t, h, "agent note")
	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "agent steer", ClientMessageID: "c-steer", Origin: domain.MessageOriginHuman, SenderSessionID: "worker-1",
	}, false)
	if err != nil || result.Steered || result.Turn.State != domain.TurnStateQueued {
		t.Fatalf("agent steer = %+v, %v, want it queued behind the hold", result, err)
	}
	assertTurnStaysQueued(t, h, "agent note")

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "user note", ClientMessageID: "c-cli", Origin: domain.MessageOriginAutomation, AuthoredByUser: true,
	}); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["agent note"] == domain.TurnStateRunning
	})
}

func TestUsageLimitHoldEndsAtTheReset(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	failUsageLimitedTurnBehindQueue(t, h, "queued note")
	agentSend := func(text string) {
		t.Helper()
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: text, ClientMessageID: text, Origin: domain.MessageOriginAutomation,
		}); err != nil {
			t.Fatal(err)
		}
	}

	h.advance(time.Minute - time.Second)
	agentSend("before the reset")
	assertTurnStaysQueued(t, h, "before the reset")

	h.advance(time.Second)
	assertTurnStaysQueued(t, h, "queued note")
	agentSend("after the reset")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
	})
}

func TestUsageLimitHoldOutlastsTheResetForAnArmedAutoResume(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	source := failUsageLimitedTurnBehindQueue(t, h, "queued note")
	if err := h.svc.SetTurnAutoResume(ctx, testSession, source.ID, true); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "another note", ClientMessageID: "c-agent", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	assertTurnStaysQueued(t, h, "queued note")

	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "finish this task" {
		t.Fatalf("provider received %v, want the limited prompt sent again first", got)
	}
}

func TestUsageLimitHoldsTheQueueOnlyBehindTheUsersPrompt(t *testing.T) {
	for _, authoredByUser := range []bool{false, true} {
		t.Run(fmt.Sprintf("authored by user %v", authoredByUser), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "automation task", ClientMessageID: "c1", Origin: domain.MessageOriginAutomation, AuthoredByUser: authoredByUser,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "queued note", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
			}); err != nil {
				t.Fatal(err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
				TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
			failedTurnSnapshot(t, h, turn.ID)

			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
				Text: "another note", ClientMessageID: "c3", Origin: domain.MessageOriginAutomation,
			}); err != nil {
				t.Fatal(err)
			}
			if authoredByUser {
				assertTurnStaysQueued(t, h, "queued note")
				return
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["queued note"] == domain.TurnStateRunning
			})
		})
	}
}

func TestUsageLimitHoldsTheQueueBehindABatchWithTheUsersPrompt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "busy", ClientMessageID: "c0", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	for _, msg := range []ports.ChatUserMessage{
		{Text: "CI note", ClientMessageID: "c1", Origin: domain.MessageOriginAutomation},
		{Text: "user note", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation, AuthoredByUser: true},
	} {
		if turn, err := h.svc.Send(ctx, testSession, msg); err != nil || turn.State != domain.TurnStateQueued {
			t.Fatalf("queue %q: %+v, %v", msg.Text, turn, err)
		}
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	var lead string
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.State == domain.TurnStateRunning {
				lead = turn.ProviderTurnID
			}
		}
		return lead != "" && len(h.conv.sentTexts()) == 2
	})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: lead,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["CI note"] == domain.TurnStateFailed
	})

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "agent note", ClientMessageID: "c3", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatal(err)
	}
	assertTurnStaysQueued(t, h, "agent note")
}

func TestUsageLimitHoldKeepsAgentSteersOffTheRunningRetry(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversationAndStore(t, provider, func(st *store.Store) chatsvc.Store { return st })
	h.conv = provider.fakeConversation
	ctx := context.Background()
	source := failUsageLimitedTurnBehindQueue(t, h, "queued note")
	retry, err := h.svc.RetryTurn(ctx, testSession, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: retry.ProviderTurnID})

	result, err := h.svc.Steer(ctx, testSession, ports.ChatUserMessage{
		Text: "agent steer", ClientMessageID: "c-steer", SenderSessionID: "worker-1",
	})
	if err != nil || len(provider.steers()) != 1 || provider.steers()[0].msg.Text != "agent steer" {
		t.Fatalf("steer = %+v, %v, provider received %+v, want only the steer", result, err, provider.steers())
	}
	assertTurnStaysQueued(t, h, "queued note")
}

func startTerminalSwitch(t *testing.T, h *harness) {
	t.Helper()
	if _, created, err := h.st.CreateSessionInterfaceTransition(context.Background(), domain.SessionInterfaceTransition{
		ID: "terminal-switch", SessionID: testSession, SourceMode: domain.SessionModeChat, TargetMode: domain.SessionModeTUI,
		Policy: domain.SessionInterfaceTransitionDrain, HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
		Phase: domain.SessionInterfaceTransitionRequested, CreatedAt: h.now(), UpdatedAt: h.now(),
	}); err != nil || !created {
		t.Fatalf("terminal switch: created=%v err=%v", created, err)
	}
}

func TestInterfaceHandoffDrainsPastANewUsageLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "finish this task", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"note 1", "note 2"} {
		if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: text, ClientMessageID: fmt.Sprintf("q%d", i), Origin: domain.MessageOriginHuman}); err != nil {
			t.Fatal(err)
		}
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	startTerminalSwitch(t, h)

	prepareCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	prepared := make(chan error, 1)
	go func() {
		prepared <- h.svc.PrepareChatHandoff(prepareCtx, testSession, domain.SessionInterfaceTransitionDrain)
	}()
	for _, step := range []struct {
		text  string
		state domain.TurnState
		err   error
	}{
		{"note 1", domain.TurnStateFailed, errors.New(timedUsageLimit)},
		{"note 2", domain.TurnStateCompleted, nil},
	} {
		var running string
		h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			running = ""
			for _, turn := range s.Turns {
				if turn.State == domain.TurnStateRunning {
					running = turn.ProviderTurnID
				}
			}
			return running != "" && turnStateByText(t, s)[step.text] == domain.TurnStateRunning
		})
		h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: running, TurnState: step.state, Err: step.err})
	}
	if err := <-prepared; err != nil {
		t.Fatalf("prepare handoff: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["note 1"] == domain.TurnStateFailed && states["note 2"] == domain.TurnStateCompleted
	})
	if held, err := h.st.ConversationQueueHeld(ctx, h.ctrl.ConversationID(), h.now()); err != nil || held {
		t.Fatalf("queue held = %v, %v after a terminal switch drained it", held, err)
	}
}
