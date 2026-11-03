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
