package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSearchMessagesAfterPagesInCursorOrder(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)

	var seen []string
	var cursorAt time.Time
	cursorID := ""
	for page := 0; page < 5; page++ {
		rows, err := s.SearchMessagesAfter(ctx, cursorAt, cursorID, 2)
		if err != nil {
			t.Fatalf("SearchMessagesAfter: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen = append(seen, row.ID)
		}
		last := rows[len(rows)-1]
		cursorAt, cursorID = last.UpdatedAt, last.ID
	}
	want := map[string]bool{"message-1": true, "message-assistant-1": true, "message-2": true}
	if len(seen) != len(want) {
		t.Fatalf("paged ids = %v, want each of %v exactly once", seen, want)
	}
	for _, id := range seen {
		if !want[id] {
			t.Fatalf("unexpected id %q in %v", id, seen)
		}
	}

	// Nothing changed since the last cursor: the catch-up is empty.
	rows, err := s.SearchMessagesAfter(ctx, cursorAt, cursorID, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("catch-up after last cursor = %v err=%v, want empty", rows, err)
	}
}

func TestSearchMessageIDsAfterListsEveryMessage(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)

	first, err := s.SearchMessageIDsAfter(ctx, "", 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %v err=%v", first, err)
	}
	rest, err := s.SearchMessageIDsAfter(ctx, first[len(first)-1], 10)
	if err != nil || len(rest) != 1 {
		t.Fatalf("second page = %v err=%v", rest, err)
	}
}

func TestSearchMessagesByIDCarriesConversationFacts(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)

	got, err := s.SearchMessagesByID(ctx, []string{"message-1", "missing"})
	if err != nil {
		t.Fatalf("SearchMessagesByID: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want only the existing one", len(got))
	}
	msg := got[0]
	if msg.Text != "first prompt" || msg.SessionID != session.ID || msg.ProjectID != "branches" || msg.ConversationID != conversation.ID {
		t.Fatalf("message = %+v", msg)
	}
	if none, err := s.SearchMessagesByID(ctx, nil); err != nil || none != nil {
		t.Fatalf("empty ids = %v err=%v", none, err)
	}
}

func TestSearchVisibleMessageIDsHidesMessagesPastTheEditFork(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)
	ids := []string{"message-1", "message-assistant-1", "message-2"}

	visible, err := s.SearchVisibleMessageIDs(ctx, conversation.ID, ids)
	if err != nil {
		t.Fatalf("SearchVisibleMessageIDs: %v", err)
	}
	for _, id := range ids {
		if !visible[id] {
			t.Fatalf("%s hidden before any edit: %v", id, visible)
		}
	}

	messages, err := s.ConversationMessages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("ConversationMessages: %v", err)
	}
	var forkAfter int64
	for _, m := range messages {
		if m.ID == "message-assistant-1" {
			forkAfter = m.Sequence
		}
	}
	// Editing the second prompt forks after the first answer: the old second
	// prompt stays stored on the root branch but leaves the visible transcript.
	branch := domain.ConversationBranch{
		ID: "branch-edit", ConversationID: conversation.ID, ParentBranchID: conversation.ActiveBranchID,
		ProviderConversationID: "thread-edit", ForkAfterSequence: forkAfter,
	}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "generation-edit", testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("CreateAndActivateConversationBranch: %v", err)
	}
	visible, err = s.SearchVisibleMessageIDs(ctx, conversation.ID, ids)
	if err != nil {
		t.Fatalf("SearchVisibleMessageIDs after edit: %v", err)
	}
	if !visible["message-1"] || !visible["message-assistant-1"] || visible["message-2"] {
		t.Fatalf("visible after edit = %v, want the forked-away prompt hidden", visible)
	}
}

func TestSearchMessagesByIDPointsReviewerMessagesAtTheirReview(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "reviewed")
	session, err := s.CreateSession(ctx, sampleRecord("reviewed"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	review := domain.Review{ID: "review-1", SessionID: session.ID, ProjectID: session.ProjectID, Harness: domain.ReviewerCodex,
		InterfaceMode: domain.ReviewerInterfaceChat, ControllerGeneration: "review-generation", CreatedAt: testNow, UpdatedAt: testNow}
	if err := s.UpsertReview(ctx, review); err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}
	conversation, err := s.CreateReviewConversation(ctx, "conversation-review", review.ID, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatalf("CreateReviewConversation: %v", err)
	}
	created, err := s.AppendReviewUserMessage(ctx, conversation.ID, session.ID, review.ID, "review-generation", domain.ConversationMessage{
		ID: "review-message", Origin: domain.MessageOriginHuman, Text: "check the retry loop", ClientMessageID: "client-review",
	}, "review-turn", testNow)
	if err != nil || !created {
		t.Fatalf("AppendReviewUserMessage: created=%v err=%v", created, err)
	}

	got, err := s.SearchMessagesByID(ctx, []string{"review-message"})
	if err != nil || len(got) != 1 {
		t.Fatalf("SearchMessagesByID = %+v err=%v", got, err)
	}
	if got[0].ReviewID != review.ID || got[0].SessionID != session.ID {
		t.Fatalf("reviewer message = %+v, want review %s on session %s", got[0], review.ID, session.ID)
	}
}
