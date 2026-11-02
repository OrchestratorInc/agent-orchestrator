package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLatestConversationTurnUsesRowOrderAndSkipsRollback(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	if _, err := s.LatestConversationTurn(ctx, conversation.ID); !errors.Is(err, domain.ErrNoConversationTurn) {
		t.Fatalf("empty conversation: %v", err)
	}
	// Both prompts share RequestedAt; SQLite row order must decide the latest.
	appendBranchPrompt(t, s, session, conversation, "generation-root", "a", "A")
	appendBranchPrompt(t, s, session, conversation, "generation-root", "b", "B")
	turn, err := s.LatestConversationTurn(ctx, conversation.ID)
	if err != nil || turn.ID != "turn-b" || turn.State != domain.TurnStateQueued {
		t.Fatalf("latest same-time turn: %+v, %v", turn, err)
	}
	if _, err := s.RollbackTurns(ctx, conversation.ID, "turn-b", testNow); err != nil {
		t.Fatal(err)
	}
	turn, err = s.LatestConversationTurn(ctx, conversation.ID)
	if err != nil || turn.ID != "turn-a" {
		t.Fatalf("latest after rollback: %+v, %v", turn, err)
	}
}

func TestLatestConversationTurnFollowsActiveBranchAndNestedCutoffs(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "a", "A")
	appendBranchPrompt(t, s, session, conversation, "generation-root", "b", "B")
	child := domain.ConversationBranch{
		ID: "latest-child", ConversationID: conversation.ID, SessionID: session.ID,
		ProviderConversationID: "thread-latest-child", ParentBranchID: conversation.ActiveBranchID,
		ForkAfterTurnID: "turn-a", ReplacedTurnID: "turn-b", ForkAfterSequence: 1,
	}
	if err := s.CreateConversationBranch(ctx, child, testNow); err != nil {
		t.Fatal(err)
	}
	activateTestBranch(t, s, session, conversation, child.ID, child.ProviderConversationID, "generation-child")
	turn, err := s.LatestConversationTurn(ctx, conversation.ID)
	if err != nil || turn.ID != "turn-a" {
		t.Fatalf("active lineage leaked replaced root turn: %+v, %v", turn, err)
	}
	appendBranchPrompt(t, s, session, conversation, "generation-child", "child", "Child prompt")
	turn, err = s.LatestConversationTurn(ctx, conversation.ID)
	if err != nil || turn.ID != "turn-child" {
		t.Fatalf("latest child turn: %+v, %v", turn, err)
	}
	// A narrower nested cutoff must hide every turn on both ancestor branches.
	nested := domain.ConversationBranch{
		ID: "latest-nested", ConversationID: conversation.ID, SessionID: session.ID,
		ProviderConversationID: "thread-latest-nested", ParentBranchID: child.ID,
		ReplacedTurnID: "turn-a", ForkAfterSequence: 0,
	}
	if err := s.CreateConversationBranch(ctx, nested, testNow); err != nil {
		t.Fatal(err)
	}
	activateTestBranch(t, s, session, conversation, nested.ID, nested.ProviderConversationID, "generation-nested")
	if _, err := s.LatestConversationTurn(ctx, conversation.ID); !errors.Is(err, domain.ErrNoConversationTurn) {
		t.Fatalf("nested cutoff leaked an ancestor turn: %v", err)
	}
}

func TestListInterruptedSessionsReadsLatestTurn(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	interrupted := func() bool {
		t.Helper()
		got, err := s.ListInterruptedSessions(ctx, []domain.SessionID{session.ID, "missing"})
		if err != nil {
			t.Fatal(err)
		}
		if got["missing"] {
			t.Fatal("a session without a conversation reported as interrupted")
		}
		return got[session.ID]
	}
	if interrupted() {
		t.Fatal("empty conversation reported as interrupted")
	}
	appendBranchPrompt(t, s, session, conversation, "generation-root", "a", "A")
	if err := s.SettleTurnByID(ctx, "turn-a", domain.TurnStateInterrupted, "", testNow); err != nil {
		t.Fatal(err)
	}
	if !interrupted() {
		t.Fatal("interrupted latest turn not reported")
	}
	// A prompt the user queued and then withdrew does not end the interruption.
	appendBranchPrompt(t, s, session, conversation, "generation-root", "b", "B")
	if err := s.CancelQueuedTurnByID(ctx, conversation.ID, "turn-b", testNow); err != nil {
		t.Fatal(err)
	}
	if !interrupted() {
		t.Fatal("withdrawn queued turn cleared the interruption")
	}
	// The next real turn supersedes it.
	appendBranchPrompt(t, s, session, conversation, "generation-root", "c", "C")
	if interrupted() {
		t.Fatal("a newer turn still reported as interrupted")
	}
}
