package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLoadConversationActivityReturnsOnlyMatchingFullRow(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)
	for _, id := range []string{"one", "two"} {
		if err := s.UpsertActivity(ctx, conversation.ID, "provider-turn-1", domain.ConversationActivity{ID: id, ProviderItemID: id, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Summary: "command", Detail: []byte(`{"command":"cat file"}`)}, testNow); err != nil {
			t.Fatal(err)
		}
	}
	full := strings.Repeat("output", 1000)
	if _, err := s.AppendCommandOutput(ctx, conversation.ID, "one", full, testNow); err != nil {
		t.Fatal(err)
	}
	row, err := s.LoadConversationActivity(ctx, conversation.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "one" || row.CommandOutput != full {
		t.Fatal("full row mismatch")
	}
	for _, tc := range [][2]string{{conversation.ID, "missing"}, {"other-conversation", "one"}} {
		_, err := s.LoadConversationActivity(ctx, tc[0], tc[1])
		if !errors.Is(err, domain.ErrConversationActivityNotFound) {
			t.Fatalf("wrong scope/missing err=%v", err)
		}
	}
}

func TestLoadConversationActivityHonorsActiveBranch(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)
	if err := s.UpsertActivity(ctx, conversation.ID, "provider-turn-1", domain.ConversationActivity{ID: "root-only", ProviderItemID: "root-only", Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusCompleted, Detail: []byte(`{"text":"root"}`)}, testNow); err != nil {
		t.Fatal(err)
	}
	child := domain.ConversationBranch{ID: "child", ConversationID: conversation.ID, SessionID: session.ID, ProviderConversationID: "thread-child", ParentBranchID: conversation.ActiveBranchID, ForkAfterTurnID: "turn-1", ReplacedTurnID: "turn-2", ForkAfterSequence: 1, Strategy: domain.ConversationBranchStrategyApproximateContext}
	if err := s.CreateConversationBranch(ctx, child, testNow); err != nil {
		t.Fatal(err)
	}
	activateTestBranch(t, s, session, conversation, child.ID, child.ProviderConversationID, "child-generation")
	if _, err := s.LoadConversationActivity(ctx, conversation.ID, "root-only"); !errors.Is(err, domain.ErrConversationActivityNotFound) {
		t.Fatalf("inactive branch activity err=%v", err)
	}
}
