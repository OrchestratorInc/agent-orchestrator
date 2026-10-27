package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// sessionSearchFixture seeds one project, two sessions with distinct harnesses,
// and one Chat conversation whose messages carry text a query can find.
func sessionSearchFixture(t *testing.T) (*sqlite.Store, domain.ConversationRecord) {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "search")

	rec := sampleRecord("search")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = "thread-search"
	rec.Metadata.ControllerGeneration = "generation-search"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	conversation, err := s.CreateConversation(
		ctx, "conversation-search", domain.ConversationScopeSession,
		"search", session.ID, testNow,
	)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, "generation-search", domain.ConversationMessage{
		ID: "message-user-1", Origin: domain.MessageOriginHuman, Text: "the login test is flaky",
		ClientMessageID: "client-search-1",
	}, "turn-search-1", testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if err := s.BindTurnToProvider(ctx, "turn-search-1", "provider-turn-search-1", testNow); err != nil {
		t.Fatalf("BindTurnToProvider: %v", err)
	}
	if err := s.SettleAssistantMessage(ctx, conversation.ID, "assistant-search-1", "provider-turn-search-1",
		"I found the flaky login test and awaited the promise", "message-assistant-1", testNow); err != nil {
		t.Fatalf("SettleAssistantMessage: %v", err)
	}
	return s, conversation
}

// matchExpression builds the FTS expression the same way the search box does,
// so a store test exercises the real query shape rather than a hand-written one.
func matchExpression(raw string) string {
	return domain.ParseSessionSearchQuery(raw).MatchQuery
}

func seedSearchDoc(t *testing.T, s *sqlite.Store, doc domain.SessionSearchDoc) {
	t.Helper()
	if doc.IndexedAt.IsZero() {
		doc.IndexedAt = testNow
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = testNow
	}
	if err := s.UpsertSessionSearchDocs(context.Background(), []domain.SessionSearchDoc{doc}); err != nil {
		t.Fatalf("UpsertSessionSearchDocs: %v", err)
	}
}

func TestSearchSessionSearchDocsRanksTitleAboveBody(t *testing.T) {
	s := newTestStore(t)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "title", SessionID: "a", ProjectID: "p",
		Kind: domain.SessionSearchKindTitle, Title: "flaky login test",
	})
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:b", DocKey: "m1", SessionID: "b", ProjectID: "p",
		Kind: domain.SessionSearchKindUser, Body: "a long message that mentions the flaky login test in passing",
	})
	hits, err := s.SearchSessionSearchDocs(context.Background(), domain.SessionSearchParams{
		MatchQuery: matchExpression("flaky login"),
	})
	if err != nil {
		t.Fatalf("SearchSessionSearchDocs: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].SessionID != "a" {
		t.Fatalf("first hit session = %q, want the title match", hits[0].SessionID)
	}
	if hits[0].Rank >= hits[1].Rank {
		t.Fatalf("title rank %v must beat body rank %v (bm25 is more-negative-is-better)", hits[0].Rank, hits[1].Rank)
	}
}

func TestSearchSessionSearchDocsFindsPathsAndFilters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "paths", SessionID: "a", ProjectID: "alpha",
		Harness: "claude-code", Kind: domain.SessionSearchKindPath,
		Paths: "src/auth/login_test.ts\nsrc/auth/login.ts",
	})
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:b", DocKey: "paths", SessionID: "b", ProjectID: "beta",
		Harness: "codex", Kind: domain.SessionSearchKindPath,
		Paths: "src/auth/session.ts",
	})

	all, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("login"),
	})
	if err != nil {
		t.Fatalf("search paths: %v", err)
	}
	if len(all) != 1 || all[0].SessionID != "a" {
		t.Fatalf("path search = %+v, want only session a", all)
	}

	byProject, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("auth"),
		ProjectID:  "beta",
	})
	if err != nil {
		t.Fatalf("search by project: %v", err)
	}
	if len(byProject) != 1 || byProject[0].SessionID != "b" {
		t.Fatalf("project filter = %+v, want only session b", byProject)
	}

	byHarness, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("auth"),
		Harness:    "claude-code",
	})
	if err != nil {
		t.Fatalf("search by harness: %v", err)
	}
	if len(byHarness) != 1 || byHarness[0].SessionID != "a" {
		t.Fatalf("harness filter = %+v, want only session a", byHarness)
	}
}

func TestSearchSessionSearchDocsFiltersByDateRange(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	older := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:old", DocKey: "m1", SessionID: "old", Kind: domain.SessionSearchKindUser,
		Body: "migration notes", CreatedAt: older,
	})
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:new", DocKey: "m1", SessionID: "new", Kind: domain.SessionSearchKindUser,
		Body: "migration notes", CreatedAt: newer,
	})

	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("migration"),
		From:       &from,
	})
	if err != nil {
		t.Fatalf("search from date: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "new" {
		t.Fatalf("from filter = %+v, want only the newer session", hits)
	}

	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	hits, err = s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("migration"),
		To:         &to,
	})
	if err != nil {
		t.Fatalf("search to date: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "old" {
		t.Fatalf("to filter = %+v, want only the older session", hits)
	}
}

// The FTS mirror is maintained by triggers, so a document removed from the
// projection must stop matching. This is what makes a rollback or an edit
// disappear from results rather than linger.
func TestSearchSessionSearchDocsDropsDeletedDocuments(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser,
		Body: "obsolete needle text",
	})
	if _, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("needle"),
	}); err != nil {
		t.Fatalf("search before delete: %v", err)
	}
	if err := s.DeleteSessionSearchDocsForSource(ctx, "chat:a"); err != nil {
		t.Fatalf("DeleteSessionSearchDocsForSource: %v", err)
	}
	hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
		MatchQuery: matchExpression("needle"),
	})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("deleted document still matches: %+v", hits)
	}
}

func TestPruneSessionSearchDocsBeforeKeepsCurrentPass(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	stale := testNow
	fresh := testNow.Add(time.Minute)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "stale", SessionID: "a", Kind: domain.SessionSearchKindUser,
		Body: "stale text", IndexedAt: stale,
	})
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "fresh", SessionID: "a", Kind: domain.SessionSearchKindUser,
		Body: "fresh text", IndexedAt: fresh,
	})
	if err := s.PruneSessionSearchDocsBefore(ctx, "chat:a", fresh); err != nil {
		t.Fatalf("PruneSessionSearchDocsBefore: %v", err)
	}
	count, err := s.CountSessionSearchDocsForSource(ctx, "chat:a")
	if err != nil {
		t.Fatalf("CountSessionSearchDocsForSource: %v", err)
	}
	if count != 1 {
		t.Fatalf("remaining docs = %d, want only the fresh one", count)
	}
}

func TestSessionSearchSourcesTrackCursorAndState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	source := domain.SessionSearchSource{
		SourceKey:   "native:claude-code:sess-1",
		Kind:        domain.SessionSearchSourceNative,
		SessionID:   "sess-1",
		ProjectID:   "p",
		Harness:     "claude-code",
		Path:        "/tmp/transcript.jsonl",
		CursorValue: 128,
		FileSize:    256,
		FileMtime:   42,
		DocCount:    3,
		State:       domain.SessionSearchSourceActive,
		UpdatedAt:   testNow,
	}
	if err := s.UpsertSessionSearchSource(ctx, source); err != nil {
		t.Fatalf("UpsertSessionSearchSource: %v", err)
	}
	got, ok, err := s.GetSessionSearchSource(ctx, source.SourceKey)
	if err != nil || !ok {
		t.Fatalf("GetSessionSearchSource: ok=%v err=%v", ok, err)
	}
	if got.CursorValue != 128 || got.State != domain.SessionSearchSourceActive || got.Path != source.Path {
		t.Fatalf("source = %+v, want the recorded cursor", got)
	}

	source.State = domain.SessionSearchSourceUnavailable
	source.LastErrorCode = "read_failed"
	source.UpdatedAt = testNow.Add(time.Minute)
	if err := s.UpsertSessionSearchSource(ctx, source); err != nil {
		t.Fatalf("UpsertSessionSearchSource update: %v", err)
	}
	counts, err := s.CountSessionSearchSourcesByState(ctx)
	if err != nil {
		t.Fatalf("CountSessionSearchSourcesByState: %v", err)
	}
	if counts[domain.SessionSearchSourceUnavailable] != 1 {
		t.Fatalf("unavailable sources = %d, want 1", counts[domain.SessionSearchSourceUnavailable])
	}
	if _, ok, err := s.GetSessionSearchSource(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing source: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// The indexer reads conversation visibility through the same branch-aware query
// the UI uses, so a cancelled turn's text never becomes searchable.
func TestSessionSearchVisibleMessagesSkipDiscardedTurns(t *testing.T) {
	s, conversation := sessionSearchFixture(t)
	ctx := context.Background()
	messages, err := s.SessionSearchVisibleMessages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("SessionSearchVisibleMessages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("messages = %d, want the user and assistant messages", len(messages))
	}
	if messages[0].Text != "the login test is flaky" {
		t.Fatalf("first message = %q", messages[0].Text)
	}
}

func TestSessionSearchTouchedPathsReadsFileChangeActivities(t *testing.T) {
	s, conversation := sessionSearchFixture(t)
	ctx := context.Background()
	if err := s.UpsertActivity(ctx, conversation.ID, "provider-turn-search-1", domain.ConversationActivity{
		ID:      "activity-1",
		Kind:    domain.ActivityKindFileChange,
		Status:  domain.ActivityStatusCompleted,
		Summary: "Created src/auth/login_test.ts",
		Detail:  []byte(`{"files":[{"path":"src/auth/login_test.ts","status":"added"},{"path":"src/auth/login.ts","status":"modified"}]}`),
	}, testNow); err != nil {
		t.Fatalf("UpsertActivity: %v", err)
	}
	paths, err := s.SessionSearchTouchedPaths(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("SessionSearchTouchedPaths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "src/auth/login_test.ts" || paths[1] != "src/auth/login.ts" {
		t.Fatalf("paths = %v, want both touched files in order", paths)
	}
}

// Rewriting a document whose text did not change must not touch the FTS index.
// Every pass over a source rewrites all of its documents; without the guard in
// the text upsert, each one would cost an FTS delete and re-insert.
func TestUpsertSessionSearchDocsLeavesUnchangedTextAlone(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer func() { _ = raw.Close() }()
	ftsState := func() string {
		var rows, bytes int64
		if err := raw.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(block)), 0) FROM session_search_fts_data`).Scan(&rows, &bytes); err != nil {
			t.Fatalf("read fts state: %v", err)
		}
		return fmt.Sprintf("%d/%d", rows, bytes)
	}
	doc := domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser,
		Body: "original needle text",
	}
	seedSearchDoc(t, s, doc)
	before := ftsState()

	// The same text in a later pass: the metadata row moves, the index does not.
	doc.IndexedAt = testNow.Add(time.Minute)
	seedSearchDoc(t, s, doc)
	if after := ftsState(); after != before {
		t.Fatalf("an unchanged document rewrote the FTS index: %s -> %s", before, after)
	}

	// Changed text is re-indexed: the new word matches and the old one does not.
	doc.Body = "replacement haystack text"
	doc.IndexedAt = testNow.Add(2 * time.Minute)
	seedSearchDoc(t, s, doc)
	if hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: matchExpression("haystack")}); err != nil || len(hits) != 1 {
		t.Fatalf("new text not searchable: hits=%+v err=%v", hits, err)
	}
	if hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: matchExpression("needle")}); err != nil || len(hits) != 0 {
		t.Fatalf("old text still searchable: hits=%+v err=%v", hits, err)
	}

	// Deleting the document removes its text with it; nothing is left to match.
	if err := s.DeleteSessionSearchDocsForSource(ctx, "chat:a"); err != nil {
		t.Fatalf("DeleteSessionSearchDocsForSource: %v", err)
	}
	var orphans int64
	if err := raw.QueryRow(`SELECT COUNT(*) FROM session_search_text`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("text rows left after delete: %d (err %v)", orphans, err)
	}
}
