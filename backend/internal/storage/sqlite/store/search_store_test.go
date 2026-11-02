package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	if err := s.SettleAssistantMessage(ctx, conversation.ID, "assistant-2", "provider-turn-2", "prompt prompt prompt walrus first first first", "message-assistant-2", testNow); err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, s, "walrus"); !slices.Equal(got, []string{"message-assistant-2"}) {
		t.Fatalf("settled walrus = %v", got)
	}
	// Words in the order typed beat a message that repeats them out of order.
	if hits, err := s.SearchConversationMessages(ctx, "first prompt"); err != nil || len(hits) != 2 || hits[0].MessageID != "message-1" {
		t.Fatalf("first prompt = %#v err=%v", hits, err)
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

func TestSearchConversationMessagesUsesSelectedConversationBeforeCandidateLimit(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	// More stale matches than the candidate page must not hide a hit in the
	// project conversation that SelectConversationBySession actually returns.
	for i := range 40 {
		created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root", domain.ConversationMessage{
			ID: fmt.Sprintf("stale-message-%d", i), Origin: domain.MessageOriginHuman, Text: "needle",
			ClientMessageID: fmt.Sprintf("stale-client-%d", i),
		}, fmt.Sprintf("stale-turn-%d", i), testNow)
		if err != nil || !created {
			t.Fatalf("append stale message %d: created=%v err=%v", i, created, err)
		}
	}
	project, err := s.CreateConversation(ctx, "project-search", domain.ConversationScopeProject, session.ProjectID, session.ID, testNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := searchMessageIDs(t, s, "needle"); len(got) != 0 {
		t.Fatalf("superseded conversation is still searchable: %v", got)
	}
	created, err := s.AppendUserMessage(ctx, project.ID, session.ID, "generation-root", domain.ConversationMessage{
		ID: "project-message", Origin: domain.MessageOriginHuman, Text: "needle", ClientMessageID: "project-client",
	}, "project-turn", testNow.Add(2*time.Minute))
	if err != nil || !created {
		t.Fatalf("append project message: created=%v err=%v", created, err)
	}
	hits, err := s.SearchConversationMessages(ctx, "needle")
	if err != nil || len(hits) != 1 || hits[0].MessageID != "project-message" || hits[0].SessionID != string(session.ID) || hits[0].ProjectID != string(session.ProjectID) {
		t.Fatalf("selected conversation hits = %#v err=%v", hits, err)
	}
}

func TestSearchConversationMessagesRoutesReviewerHitsToTheirWorker(t *testing.T) {
	ctx := context.Background()
	s, session, _ := seededChatConversation(t)
	review := domain.Review{
		ID: "search-review", SessionID: session.ID, ProjectID: session.ProjectID,
		Harness: domain.ReviewerCodex, CreatedAt: testNow, UpdatedAt: testNow,
	}
	if err := s.UpsertReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateReviewConversation(ctx, "search-review-conversation", review.ID, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimReviewChatController(ctx, review.ID, "review-provider", "review-generation", testNow); err != nil || !claimed {
		t.Fatalf("claim reviewer: claimed=%v err=%v", claimed, err)
	}
	created, err := s.AppendReviewUserMessage(ctx, conversation.ID, session.ID, review.ID, "review-generation", domain.ConversationMessage{
		ID: "review-search-message", Text: "searchable reviewer passage", Origin: domain.MessageOriginHuman,
	}, "review-search-turn", testNow)
	if err != nil || !created {
		t.Fatalf("append reviewer message: created=%v err=%v", created, err)
	}
	hits, err := s.SearchConversationMessages(ctx, "reviewer passage")
	if err != nil || len(hits) != 1 || hits[0].ReviewID != review.ID || hits[0].SessionID != string(session.ID) || hits[0].ProjectID != string(session.ProjectID) {
		t.Fatalf("reviewer hits = %#v err=%v", hits, err)
	}
	// Reviewer Chat remains reachable even when the worker uses a terminal.
	if changed, err := s.CommitSessionControllerEpoch(ctx, session.ID, domain.SessionModeChat, domain.SessionModeTUI, session.Metadata.ProviderConversationID, testNow.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("switch worker to terminal: changed=%v err=%v", changed, err)
	}
	if got := searchMessageIDs(t, s, "reviewer passage"); !slices.Equal(got, []string{"review-search-message"}) {
		t.Fatalf("reviewer of a terminal worker = %v", got)
	}
	if changed, err := s.SetReviewInterfaceMode(ctx, review.ID, domain.ReviewerInterfaceTUI, testNow.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("switch reviewer to terminal: changed=%v err=%v", changed, err)
	}
	if got := searchMessageIDs(t, s, "reviewer passage"); len(got) != 0 {
		t.Fatalf("terminal reviewer has no Chat transcript for hits: %v", got)
	}

}

func TestSearchConversationMessagesHidesSessionChatInTerminalMode(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	seedBranchTurns(t, s, session, conversation)
	if changed, err := s.CommitSessionControllerEpoch(ctx, session.ID, domain.SessionModeChat, domain.SessionModeTUI, session.Metadata.ProviderConversationID, testNow.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("switch worker to terminal: changed=%v err=%v", changed, err)
	}
	if got := searchMessageIDs(t, s, "prompt"); len(got) != 0 {
		t.Fatalf("terminal session has no Chat transcript for hits: %v", got)
	}
}

func TestSearchConversationMessagesFiltersHiddenTranscriptBeforeCandidateLimit(t *testing.T) {
	for _, hidden := range []string{"edited branch", "rolled-back turns"} {
		t.Run(hidden, func(t *testing.T) {
			ctx := context.Background()
			s, session, conversation := seededChatConversation(t)
			// Short hidden messages rank above this longer visible message. There
			// are more of them than the candidate page, so filtering afterwards
			// would return no hits even though the transcript contains a match.
			created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root", domain.ConversationMessage{
				ID: "visible-message", Origin: domain.MessageOriginHuman, Text: "needle " + strings.Repeat("context ", 100),
				ClientMessageID: "visible-client",
			}, "visible-turn", testNow)
			if err != nil || !created {
				t.Fatalf("append visible message: created=%v err=%v", created, err)
			}
			for i := range 40 {
				created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-root", domain.ConversationMessage{
					ID: fmt.Sprintf("hidden-message-%d", i), Origin: domain.MessageOriginHuman, Text: "needle",
					ClientMessageID: fmt.Sprintf("hidden-client-%d", i),
				}, fmt.Sprintf("hidden-turn-%d", i), testNow.Add(time.Minute))
				if err != nil || !created {
					t.Fatalf("append hidden message %d: created=%v err=%v", i, created, err)
				}
			}
			if hidden == "edited branch" {
				branch := domain.ConversationBranch{
					ID: "search-edit", ConversationID: conversation.ID, ParentBranchID: conversation.ActiveBranchID,
					ForkAfterSequence: 1,
				}
				if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "search-edit-generation", testNow.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
			} else {
				if discarded, err := s.RollbackTurns(ctx, conversation.ID, "hidden-turn-0", testNow.Add(2*time.Minute)); err != nil || discarded != 40 {
					t.Fatalf("roll back hidden turns: discarded=%d err=%v", discarded, err)
				}
			}
			messages, err := s.ConversationMessages(ctx, conversation.ID)
			if err != nil || len(messages) != 1 || messages[0].ID != "visible-message" {
				t.Fatalf("visible transcript = %#v err=%v", messages, err)
			}
			if got := searchMessageIDs(t, s, "needle"); !slices.Equal(got, []string{"visible-message"}) {
				t.Fatalf("visible search hit starved by hidden matches: %v", got)
			}
		})
	}
}
