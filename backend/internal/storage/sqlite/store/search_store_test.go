package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func searchMessageIDs(t *testing.T, s *sqlite.Store, query string) []string {
	t.Helper()
	hits, err := s.SearchConversationMessages(context.Background(), query)
	if err != nil {
		t.Fatalf("SearchConversationMessages(%q): %v", query, err)
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.MessageID)
	}
	slices.Sort(ids)
	return ids
}

func TestSearchConversationMessagesFollowsTranscript(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)

	if got := searchMessageIDs(t, s, "prompt"); !slices.Equal(got, []string{"message-1", "message-2"}) {
		t.Fatalf("prompt = %v", got)
	}
	// Every word, any order, by prefix and word form.
	if got := searchMessageIDs(t, s, "answers fir"); !slices.Equal(got, []string{"message-assistant-1"}) {
		t.Fatalf("answers fir = %v", got)
	}
	// A misspelled word falls back to any word.
	if got := searchMessageIDs(t, s, "secnd prompt"); !slices.Equal(got, []string{"message-1", "message-2"}) {
		t.Fatalf("secnd prompt = %v", got)
	}
	// FTS syntax in the query is plain text.
	if got := searchMessageIDs(t, s, `second" OR NEAR(*`); !slices.Equal(got, []string{"message-2"}) {
		t.Fatalf("operators = %v", got)
	}

	hits, err := s.SearchConversationMessages(ctx, "first answer")
	if err != nil || len(hits) != 1 {
		t.Fatalf("first answer = %#v err=%v", hits, err)
	}
	if hit := hits[0]; hit.SessionID != string(session.ID) || hit.ProjectID != "branches" || hit.Snippet != "\x02first\x03 \x02answer\x03" {
		t.Fatalf("hit = %#v", hit)
	}

	// A reply is searchable once it finishes streaming, not while it streams.
	if err := s.BindTurnToProvider(ctx, "turn-2", "provider-turn-2", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAssistantDelta(ctx, conversation.ID, "assistant-2", "provider-turn-2", "streaming walrus", "message-assistant-2", testNow); err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, s, "walrus"); len(got) != 0 {
		t.Fatalf("streaming walrus = %v", got)
	}
	if err := s.SettleAssistantMessage(ctx, conversation.ID, "assistant-2", "provider-turn-2", "settled walrus", "message-assistant-2", testNow); err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, s, "walrus"); !slices.Equal(got, []string{"message-assistant-2"}) {
		t.Fatalf("settled walrus = %v", got)
	}
	if got := searchMessageIDs(t, s, "streaming"); len(got) != 0 {
		t.Fatalf("replaced text still matches: %v", got)
	}

	// Editing the second prompt forks after the first answer: the second turn
	// leaves the transcript and the search.
	messages, err := s.ConversationMessages(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	var forkAfter int64
	for _, message := range messages {
		if message.ID == "message-assistant-1" {
			forkAfter = message.Sequence
		}
	}
	branch := domain.ConversationBranch{ID: "edit", ConversationID: conversation.ID, ParentBranchID: conversation.ActiveBranchID, ForkAfterSequence: forkAfter}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "edit-generation", testNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, s, "prompt"); !slices.Equal(got, []string{"message-1"}) {
		t.Fatalf("after edit, prompt = %v", got)
	}
	if got := searchMessageIDs(t, s, "walrus"); len(got) != 0 {
		t.Fatalf("after edit, walrus = %v", got)
	}

}

// The index follows edits and deletes of the messages it was built from;
// FTS5's integrity check compares it with conversation_messages.
func TestConversationMessagesSearchIndexStaysInSync(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedProject(t, s, "branches")
	rec := sampleRecord("branches")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversation(ctx, "conversation-branches", domain.ConversationScopeSession, "branches", session.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	seedBranchTurns(t, s, session, conversation)

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	for _, statement := range []string{
		`UPDATE conversation_messages SET text = 'rewritten walrus' WHERE id = 'message-1'`,
		`DELETE FROM conversation_messages WHERE id = 'message-assistant-1'`,
		// Every message is finished here, so the index must match the table exactly.
		`INSERT INTO conversation_messages_fts(conversation_messages_fts, rank) VALUES ('integrity-check', 1)`,
		// A message going back to streaming leaves the index.
		`UPDATE conversation_messages SET streaming = 1 WHERE id = 'message-2'`,
		`INSERT INTO conversation_messages_fts(conversation_messages_fts) VALUES ('integrity-check')`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if got := searchMessageIDs(t, s, "walrus"); !slices.Equal(got, []string{"message-1"}) {
		t.Fatalf("walrus = %v", got)
	}
	if got := searchMessageIDs(t, s, "prompt answer"); len(got) != 0 {
		t.Fatalf("stale terms still match: %v", got)
	}
}

func TestSearchConversationMessagesIgnoresEmptyQuery(t *testing.T) {
	s, _, _ := seededChatConversation(t)
	for _, query := range []string{"", "  ", `"*()`} {
		if got := searchMessageIDs(t, s, query); len(got) != 0 {
			t.Fatalf("%q = %v", query, got)
		}
	}
}
