package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFindSourceFollowsActiveBranchAndExcludesQueue(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)
	_, messages, err := s.ConversationFindSource(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	if slices.Contains(ids, "message-2") {
		t.Fatalf("queued message visible: %v", ids)
	}
	if !slices.Contains(ids, "message-assistant-1") {
		t.Fatalf("history missing: %v", ids)
	}
	var sequence int64
	for _, message := range messages {
		if message.ID == "message-1" {
			sequence = message.Sequence
		}
	}
	window, err := s.ConversationFindWindow(ctx, conversation.ID, sequence)
	if err != nil || !slices.ContainsFunc(window.Messages, func(message domain.ConversationMessage) bool { return message.ID == "message-1" }) {
		t.Fatalf("window=%#v, err=%v", window, err)
	}
	branch := domain.ConversationBranch{ID: "find-edit", ConversationID: conversation.ID, ParentBranchID: conversation.ActiveBranchID, ForkAfterSequence: sequence}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "find-generation", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, messages, err = s.ConversationFindSource(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Sequence > sequence {
			t.Fatalf("inactive branch leaked: %#v", message)
		}
	}
}
