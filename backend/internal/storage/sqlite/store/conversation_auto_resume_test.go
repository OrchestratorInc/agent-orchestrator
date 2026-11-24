package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSetTurnAutoResumeRefusalPreservesConsumedConsent(t *testing.T) {
	for _, priorError := range []string{"", "original refusal"} {
		name := "cancelled"
		if priorError != "" {
			name = "already refused"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, session, conversation := seededChatConversation(t)
			appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
			if err := s.SettleTurnByID(ctx, "turn-limited", domain.TurnStateFailed, "Usage limit reached; resets in 1 hour", testNow); err != nil {
				t.Fatal(err)
			}
			reset := testNow.Add(time.Hour)
			if err := s.SetTurnAutoResume(ctx, conversation.ID, "turn-limited", &reset, true, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.SetTurnAutoResume(ctx, conversation.ID, "turn-limited", nil, false, priorError); err != nil {
				t.Fatal(err)
			}
			staleReset := reset.Add(time.Hour)
			if err := s.SetTurnAutoResume(ctx, conversation.ID, "turn-limited", &staleReset, false, "stale scheduler refusal"); err != nil {
				t.Fatal(err)
			}
			source, err := s.TurnByID(ctx, "turn-limited")
			if err != nil || source.UsageLimit == nil {
				t.Fatalf("source = %+v, %v", source, err)
			}
			limit := source.UsageLimit
			if limit.AutoResume || limit.ResumeError != priorError || limit.ResetsAt == nil || !limit.ResetsAt.Equal(reset) {
				t.Fatalf("stale refusal changed consumed intent: %+v", limit)
			}
		})
	}
}

// ACP history replays a failed turn without its error. The live refusal is what
// classifies a usage limit, so the replay must not erase it.
func TestSettleTurnReplayKeepsUsageLimitRefusal(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
	if err := s.BindTurnToProvider(ctx, "turn-limited", "provider-limited", testNow); err != nil {
		t.Fatal(err)
	}
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	if err := s.SettleTurn(ctx, conversation.ID, "provider-limited", domain.TurnStateFailed, refusal, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurn(ctx, conversation.ID, "provider-limited", domain.TurnStateFailed, "", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	source, err := s.TurnByID(ctx, "turn-limited")
	if err != nil || source.ErrorMessage != refusal || source.UsageLimit == nil || source.UsageLimit.ResetsAt == nil {
		t.Fatalf("replay erased the usage-limit refusal: %+v, %v", source, err)
	}
}

func TestUsageLimitHoldIsNotRestoredByAReplay(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
	mustNoError(t, s.BindTurnToProvider(ctx, "turn-limited", "provider-limited", testNow))
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	mustNoError(t, s.SettleTurn(ctx, conversation.ID, "provider-limited", domain.TurnStateFailed, refusal, testNow))
	if held, err := s.ConversationQueueHeld(ctx, conversation.ID); err != nil || !held {
		t.Fatalf("queue held = %v, %v after a usage-limit failure", held, err)
	}
	mustNoError(t, s.ReleaseConversationQueue(ctx, conversation.ID))
	mustNoError(t, s.SettleTurn(ctx, conversation.ID, "provider-limited", domain.TurnStateFailed, "", testNow.Add(time.Minute)))
	if held, err := s.ConversationQueueHeld(ctx, conversation.ID); err != nil || held {
		t.Fatalf("queue held = %v, %v after replaying the released failure", held, err)
	}
}

func TestUsageLimitRefusedAtSendHoldsTheQueue(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	mustNoError(t, s.SettleTurnByID(ctx, "turn-limited", domain.TurnStateFailed, refusal, testNow))
	if held, err := s.ConversationQueueHeld(ctx, conversation.ID); err != nil || !held {
		t.Fatalf("queue held = %v, %v after a usage limit refused at send", held, err)
	}
}

func TestOnlyAUsageLimitFailureHoldsTheQueue(t *testing.T) {
	ctx := context.Background()
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	for _, tc := range []struct {
		name    string
		state   domain.TurnState
		message string
		held    bool
	}{
		{"usage limit", domain.TurnStateFailed, refusal, true},
		{"another failure", domain.TurnStateFailed, "API Error: 529 overloaded", false},
		{"interrupted", domain.TurnStateInterrupted, refusal, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, session, conversation := seededChatConversation(t)
			appendBranchPrompt(t, s, session, conversation, "generation-root", "prompt", "finish this task")
			mustNoError(t, s.BindTurnToProvider(ctx, "turn-prompt", "provider-prompt", testNow))
			mustNoError(t, s.SettleTurn(ctx, conversation.ID, "provider-prompt", tc.state, tc.message, testNow))
			if held, err := s.ConversationQueueHeld(ctx, conversation.ID); err != nil || held != tc.held {
				t.Fatalf("queue held = %v, %v, want %v", held, err, tc.held)
			}
		})
	}
}

func TestCleanupKeepsOnlyTheHeldConversationsQueue(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	other, err := s.CreateConversation(ctx, "conversation-other", domain.ConversationScopeProject, "branches", session.ID, testNow)
	mustNoError(t, err)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	mustNoError(t, s.SettleTurnByID(ctx, "turn-limited", domain.TurnStateFailed, refusal, testNow))
	for id, queued := range map[string]string{conversation.ID: "held", other.ID: "elsewhere"} {
		if _, err := s.AppendUserMessage(ctx, id, session.ID, "generation-root", domain.ConversationMessage{
			ID: "message-" + queued, Origin: domain.MessageOriginAutomation, Text: queued, ClientMessageID: "client-" + queued,
		}, "turn-"+queued, testNow); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := func(heldState domain.TurnState) {
		t.Helper()
		if owned, err := s.CleanupOwnedControllerWorkKeepingHeldQueue(ctx, session.ID, conversation.ID, "generation-root", testNow); err != nil || !owned {
			t.Fatalf("cleanup = %v, %v", owned, err)
		}
		for id, want := range map[string]domain.TurnState{"turn-held": heldState, "turn-elsewhere": domain.TurnStateFailed} {
			if turn, err := s.TurnByID(ctx, id); err != nil || turn.State != want {
				t.Fatalf("%s = %q, %v, want %q", id, turn.State, err, want)
			}
		}
	}
	cleanup(domain.TurnStateQueued)
	mustNoError(t, s.ReleaseConversationQueue(ctx, conversation.ID))
	cleanup(domain.TurnStateFailed)
}

func TestQueueHoldFollowsTheTurnThatOwnsIt(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	for i, name := range []string{"older", "limited", "later", "newest"} {
		created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root", domain.ConversationMessage{
			ID: "message-" + name, Origin: domain.MessageOriginHuman, Text: name, ClientMessageID: "client-" + name,
		}, "turn-"+name, testNow.Add(time.Duration(i)*time.Minute))
		if err != nil || !created {
			t.Fatalf("append %s: created=%v err=%v", name, created, err)
		}
		mustNoError(t, s.BindTurnToProvider(ctx, "turn-"+name, "provider-"+name, testNow))
	}
	const refusal = "You've hit your session limit · resets 12pm (Asia/Calcutta)"
	held := func(want bool, step string) {
		t.Helper()
		if got, err := s.ConversationQueueHeld(ctx, conversation.ID); err != nil || got != want {
			t.Fatalf("queue held = %v, %v after %s, want %v", got, err, step, want)
		}
	}
	later := testNow.Add(time.Hour)
	mustNoError(t, s.SettleTurn(ctx, conversation.ID, "provider-limited", domain.TurnStateFailed, refusal, later))
	held(true, "the usage limit")
	mustNoError(t, s.ReleaseConversationQueueAfterTurn(ctx, conversation.ID, "provider-older"))
	held(true, "an older turn completed")
	mustNoError(t, s.ReleaseConversationQueueAfterTurn(ctx, conversation.ID, "provider-later"))
	held(false, "a later turn completed")

	mustNoError(t, s.SettleTurn(ctx, conversation.ID, "provider-newest", domain.TurnStateFailed, refusal, later))
	mustNoError(t, s.ReleaseConversationQueueAfterTurn(ctx, conversation.ID, "provider-later"))
	held(true, "a replayed completion")
	mustNoError(t, s.ReleaseConversationQueueHeldBy(ctx, conversation.ID, "turn-limited"))
	held(true, "releasing another turn's hold")
	mustNoError(t, s.ReleaseConversationQueueHeldBy(ctx, conversation.ID, "turn-newest"))
	held(false, "releasing its own hold")
}

// A resumed Codex thread replays a turn the dead controller left in progress as
// interrupted, with no error. Nobody interrupted it, so the failure must stay.
func TestSettleTurnInterruptedReplayKeepsStoredFailure(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "crashed", "finish this task")
	if err := s.BindTurnToProvider(ctx, "turn-crashed", "provider-crashed", testNow); err != nil {
		t.Fatal(err)
	}
	const reason = "controller ended before the turn completed"
	if err := s.SettleTurn(ctx, conversation.ID, "provider-crashed", domain.TurnStateFailed, reason, testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurn(ctx, conversation.ID, "provider-crashed", domain.TurnStateInterrupted, "", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	turn, err := s.TurnByID(ctx, "turn-crashed")
	if err != nil || turn.State != domain.TurnStateFailed || turn.ErrorMessage != reason {
		t.Fatalf("interrupted replay replaced the stored failure: %+v, %v", turn, err)
	}
}

func TestAppendAutoResumeRetryRequiresDueConsent(t *testing.T) {
	for _, state := range []string{"due", "future", "cancelled", "changed account", "failed insert"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			session, conversation := seedAccountAutoResume(t, s, domain.HarnessCodex)
			sourceID := "turn-" + string(session.ID)
			now := testNow.Add(2 * time.Hour)
			msg := domain.ConversationMessage{ID: "auto-message", Origin: domain.MessageOriginHuman, Text: "finish this task"}
			switch state {
			case "future":
				now = testNow.Add(30 * time.Minute)
			case "cancelled":
				mustNoError(t, s.SetTurnAutoResume(ctx, conversation.ID, sourceID, nil, false, ""))
			case "changed account":
				mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderCodex, "account-a", testNow.Add(-time.Minute)))
				mustNoError(t, s.BindSessionAgentAccount(ctx, session.ID, domain.AgentAccountProviderCodex, "account-b", testNow.Add(time.Minute)))
			case "failed insert":
				msg.ID = "message-" + string(session.ID)
			}
			created, err := s.AppendAutoResumeRetryUserMessage(ctx, conversation.ID, session.ID, "generation-root", msg, "auto-retry", sourceID, now)
			if state == "due" {
				if err != nil || !created {
					t.Fatalf("due retry: created=%v err=%v", created, err)
				}
			} else if err == nil || created {
				t.Fatalf("ineligible retry: created=%v err=%v", created, err)
			}
			if state == "changed account" && !errors.Is(err, ports.ErrAgentAccountChanged) {
				t.Fatalf("changed account error = %v", err)
			}
			if state == "future" && !errors.Is(err, domain.ErrConversationAutoResumeNotDue) {
				t.Fatalf("future reset error = %v", err)
			}
			_, found, lookupErr := s.RetryTurnIDForSource(ctx, conversation.ID, sourceID)
			if lookupErr != nil || found != (state == "due") {
				t.Fatalf("retry receipt: found=%v err=%v", found, lookupErr)
			}
			source, err := s.TurnByID(ctx, sourceID)
			if err != nil {
				t.Fatal(err)
			}
			wantArmed := state == "future" || state == "failed insert"
			if source.UsageLimit.AutoResume != wantArmed {
				t.Fatalf("consent=%v want=%v", source.UsageLimit.AutoResume, wantArmed)
			}
		})
	}
}

func TestSetTurnAutoResumeRefusalValidatesTurnOwnership(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "limited", "finish this task")
	for _, tc := range []struct {
		name, conversationID, turnID string
	}{
		{"missing turn", conversation.ID, "missing"},
		{"wrong conversation", "other-conversation", "turn-limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetTurnAutoResume(ctx, tc.conversationID, tc.turnID, nil, false, "refused"); !errors.Is(err, domain.ErrNoConversationTurn) {
				t.Fatalf("refusal = %v, want ErrNoConversationTurn", err)
			}
		})
	}
}

func TestRetryPromptDistinguishesMissingPromptFromReadFailure(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "failed", "retry this task")
	if err := s.SettleTurnByID(ctx, "turn-failed", domain.TurnStateFailed, "source failed", testNow); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, conversationID, turnID string
	}{
		{"missing turn", conversation.ID, "missing"},
		{"wrong conversation", "other-conversation", "turn-failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.RetryPrompt(ctx, tc.conversationID, tc.turnID); !errors.Is(err, domain.ErrNoConversationTurn) {
				t.Fatalf("RetryPrompt = %v, want ErrNoConversationTurn", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.RetryPrompt(cancelled, conversation.ID, "turn-failed"); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrNoConversationTurn) {
		t.Fatalf("RetryPrompt = %v, want preserved context cancellation", err)
	}
}
