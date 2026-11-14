package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCopyForkMessageContentRestoresImages(t *testing.T) {
	s, source, sourceConv := conversationFixture(t)
	ctx := context.Background()
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	forkSession, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	forkConv, err := s.CreateConversation(ctx, "conv-fork", domain.ConversationScopeSession, "hist", forkSession.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, forkSession.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	image := `[{"type":"image","data":"iVBOR","mimeType":"image/png"}]`
	add := func(conv string, session domain.SessionID, id, text, content string, i int) {
		t.Helper()
		msg := domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman, DeliveryContentJSON: content}
		if _, err := s.AppendUserMessage(ctx, conv, session, "gen-1", msg, "turn-"+id, histClock.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	add(sourceConv, source, "s1", "Write a report", "", 1)
	add(sourceConv, source, "s2", "What colour is this?", image, 2)
	add(sourceConv, source, "s3", "Later message", image, 3)
	// The fork imported the agent's own history, where an image is text.
	add(forkConv.ID, forkSession.ID, "f1", "Write a report", "", 1)
	add(forkConv.ID, forkSession.ID, "f2", "What colour is this?[Image]", "", 2)

	changed, err := s.CopyForkMessageContent(ctx, sourceConv, forkConv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d, want 1", changed)
	}
	rows, err := s.LoadConversationSnapshot(ctx, forkConv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range rows.Messages {
		if m.ID == "f2" && (m.Text != "What colour is this?" || m.DeliveryContentJSON != image) {
			t.Fatalf("f2 = %q %q", m.Text, m.DeliveryContentJSON)
		}
		if m.ID == "f1" && m.DeliveryContentJSON != "" {
			t.Fatalf("f1 gained content: %q", m.DeliveryContentJSON)
		}
	}
}

// A rolled-back prompt isn't in the fork's history, so its image must not be
// taken for a later prompt with the same text.
func TestCopyForkMessageContentSkipsRolledBackMessages(t *testing.T) {
	s, source, sourceConv := conversationFixture(t)
	ctx := context.Background()
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	forkSession, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	forkConv, err := s.CreateConversation(ctx, "conv-fork", domain.ConversationScopeSession, "hist", forkSession.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, forkSession.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	discarded := `[{"type":"image","data":"DISCARDED","mimeType":"image/png"}]`
	kept := `[{"type":"image","data":"KEPT","mimeType":"image/png"}]`
	add := func(conv string, session domain.SessionID, id, text, content string, i int) {
		t.Helper()
		msg := domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman, DeliveryContentJSON: content}
		if _, err := s.AppendUserMessage(ctx, conv, session, "gen-1", msg, "turn-"+id, histClock.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	add(sourceConv, source, "s1", "What colour is this?", discarded, 1)
	if _, err := s.RollbackTurns(ctx, sourceConv, "turn-s1", histClock.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	add(sourceConv, source, "s2", "What colour is this?", kept, 3)
	add(forkConv.ID, forkSession.ID, "f1", "What colour is this?[Image]", "", 1)

	if _, err := s.CopyForkMessageContent(ctx, sourceConv, forkConv.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.LoadConversationSnapshot(ctx, forkConv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range rows.Messages {
		if m.ID == "f1" && m.DeliveryContentJSON != kept {
			t.Fatalf("f1 content = %q, want the retained prompt's image", m.DeliveryContentJSON)
		}
	}
}
