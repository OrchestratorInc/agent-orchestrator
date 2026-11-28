package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRewriteForkArtifactLinks(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	texts := map[string]string{
		"m1": "See ao://sessions/p/p-1/artifacts/report.html and ao://sessions/p/p-1/artifacts/a/b.png",
		"m2": "The original is ao://sessions/p/p-1 itself.",
		"m3": "Nothing to change.",
	}
	i := 0
	for id, text := range texts {
		i++
		if _, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman}, "turn-"+id, histClock.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := s.RewriteConversationText(ctx, conversation, "ao://sessions/p/p-1/artifacts/", "ao://sessions/p/p-9/artifacts/")
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d, want 1", changed)
	}
	rows, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range rows.Messages {
		got[m.ID] = m.Text
	}
	if got["m1"] != "See ao://sessions/p/p-9/artifacts/report.html and ao://sessions/p/p-9/artifacts/a/b.png" {
		t.Fatalf("m1 = %q", got["m1"])
	}
	if got["m2"] != texts["m2"] || got["m3"] != texts["m3"] {
		t.Fatalf("other rows changed: %q / %q", got["m2"], got["m3"])
	}
}
