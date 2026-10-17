package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
