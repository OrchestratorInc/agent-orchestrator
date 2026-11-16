package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// A source with more documents than one delete batch is cleared in several
// short transactions, and nothing of it is left.
func TestDeleteSessionSearchDocsForSourceClearsInBatches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	docs := make([]domain.SessionSearchDoc, 100)
	for i := range docs {
		docs[i] = domain.SessionSearchDoc{
			SourceKey: "chat:big", DocKey: fmt.Sprint(i), SessionID: "big", Kind: domain.SessionSearchKindUser,
			Body: fmt.Sprintf("line %d of a long session", i), CreatedAt: testNow, IndexedAt: testNow,
		}
	}
	if err := s.UpsertSessionSearchDocs(ctx, docs); err != nil {
		t.Fatalf("UpsertSessionSearchDocs: %v", err)
	}
	if err := s.DeleteSessionSearchDocsForSource(ctx, "chat:big"); err != nil {
		t.Fatalf("DeleteSessionSearchDocsForSource: %v", err)
	}
	if count, err := s.CountSessionSearchDocsForSource(ctx, "chat:big"); err != nil || count != 0 {
		t.Fatalf("remaining docs = %d (err %v), want none", count, err)
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
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "search.db"))
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

// Found in review: stage one keeps the best 2,000 documents, so a page past
// them came back empty while the total still counted them, and the remaining
// pages could never be fetched. Such a page is read again without the cap.
func TestSearchSessionSearchDocsPagesPastTheCandidateCap(t *testing.T) {
	s := newTestStore(t)
	const matching = 2050
	docs := make([]domain.SessionSearchDoc, 0, matching)
	for i := range matching {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: fmt.Sprintf("chat:s%04d", i), DocKey: "m1", SessionID: fmt.Sprintf("s%04d", i), ProjectID: "p",
			Kind: domain.SessionSearchKindUser, Body: "the zzcapped word", IndexedAt: testNow, CreatedAt: testNow,
		})
	}
	if err := s.UpsertSessionSearchDocs(context.Background(), docs); err != nil {
		t.Fatalf("UpsertSessionSearchDocs: %v", err)
	}
	for _, group := range []domain.SessionSearchGroup{domain.SessionSearchGroupSession, domain.SessionSearchGroupMessage} {
		first, err := s.SearchSessionSearchDocs(context.Background(), domain.SessionSearchParams{
			MatchQuery: matchExpression("zzcapped"), Limit: 100, Group: group, SkipSnippets: true,
		})
		if err != nil {
			t.Fatalf("[%s] first page: %v", group, err)
		}
		// The first pages stay capped: that is what keeps a broad search fast.
		if len(first) != 100 || !first[0].MatchTotalCapped {
			t.Fatalf("[%s] first page = %d rows, capped=%v; want 100 from the capped candidates", group, len(first), len(first) > 0 && first[0].MatchTotalCapped)
		}
		last, err := s.SearchSessionSearchDocs(context.Background(), domain.SessionSearchParams{
			MatchQuery: matchExpression("zzcapped"), Limit: 100, Offset: 2000, Group: group, SkipSnippets: true,
		})
		if err != nil {
			t.Fatalf("[%s] page past the cap: %v", group, err)
		}
		if len(last) != matching-2000 {
			t.Fatalf("[%s] page past the cap = %d rows, want %d", group, len(last), matching-2000)
		}
	}
}

// Found in review: re-ranking a late page without the cap changed the order
// between pages. The cap picks by raw bm25, the final order also weighs kinds,
// so 2,000 short assistant replies filled the cap while 50 longer user
// messages, which the user weight ranks first, were left out. The page past
// the cap repeated replies already shown and the user messages were never
// reached. Paging through everything must return every result exactly once.
func TestSearchSessionSearchDocsPagingReturnsEveryResultOnceAcrossTheCap(t *testing.T) {
	s := newTestStore(t)
	docs := make([]domain.SessionSearchDoc, 0, 2050)
	for i := range 2000 {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: fmt.Sprintf("chat:a%04d", i), DocKey: "m1", SessionID: fmt.Sprintf("a%04d", i), ProjectID: "p",
			Kind: domain.SessionSearchKindAssistant, Body: "zzmixed alpha", IndexedAt: testNow, CreatedAt: testNow,
		})
	}
	for i := range 50 {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: fmt.Sprintf("chat:u%04d", i), DocKey: "m1", SessionID: fmt.Sprintf("u%04d", i), ProjectID: "p",
			Kind: domain.SessionSearchKindUser, Body: "zzmixed alpha beta", IndexedAt: testNow, CreatedAt: testNow,
		})
	}
	if err := s.UpsertSessionSearchDocs(context.Background(), docs); err != nil {
		t.Fatalf("UpsertSessionSearchDocs: %v", err)
	}
	for _, group := range []domain.SessionSearchGroup{domain.SessionSearchGroupSession, domain.SessionSearchGroupMessage} {
		seen := map[string]int{}
		for offset := 0; offset < 2200; offset += 100 {
			page, err := s.SearchSessionSearchDocs(context.Background(), domain.SessionSearchParams{
				MatchQuery: matchExpression("zzmixed"), Limit: 100, Offset: offset, Group: group, SkipSnippets: true,
			})
			if err != nil {
				t.Fatalf("[%s] offset %d: %v", group, offset, err)
			}
			for _, hit := range page {
				seen[hit.SessionID]++
			}
		}
		repeated := 0
		for _, count := range seen {
			if count > 1 {
				repeated++
			}
		}
		if len(seen) != len(docs) || repeated != 0 {
			t.Fatalf("[%s] paging returned %d distinct results (%d repeated), want all %d exactly once", group, len(seen), repeated, len(docs))
		}
	}
}

// The text is stored once, compressed, and snippets are cut from it: the
// result page and the find in chat's match list both still mark the matched word.
func TestSessionSearchStoresTextCompressedAndCutsSnippetsFromIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "search.db"))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	defer func() { _ = raw.Close() }()
	output := strings.Repeat("ok  	pkg/module/subpackage	0.412s\n", 200) + "FAIL zzCompressedNeedle timed out after 30s\n"
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:c", DocKey: "activity:1", SessionID: "c", Kind: domain.SessionSearchKindCommand, Body: output,
	})
	var stored int64
	if err := raw.QueryRow(`SELECT LENGTH(body) FROM session_search_text`).Scan(&stored); err != nil {
		t.Fatalf("read stored body: %v", err)
	}
	if stored*3 > int64(len(output)) {
		t.Fatalf("stored %d bytes for %d bytes of repetitive output, want it compressed", stored, len(output))
	}
	parsed := domain.ParseSessionSearchQuery("zzCompressedNeedle")
	hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: parsed.MatchQuery, HighlightTerms: parsed.HighlightTerms})
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: hits=%+v err=%v", hits, err)
	}
	marked := domain.SessionSearchSnippetStart + "zzCompressedNeedle" + domain.SessionSearchSnippetEnd
	if !strings.Contains(hits[0].Snippet, marked) {
		t.Fatalf("result snippet %q does not mark the match", hits[0].Snippet)
	}
}

// In a contentless index, removing rows by rowid
// (contentless_delete) left them counting in term statistics, so after a
// source was cleared or a document rewritten, bm25 scored the remaining
// documents differently from a fresh index (-7.27 became -12.21 here).
// Rows are removed with FTS5's 'delete' command and their indexed text now,
// which keeps the statistics exact.
func TestSessionSearchScoresAreUnchangedByDeletedDocuments(t *testing.T) {
	ctx := context.Background()
	corpus := func(source string, n int, filler string) []domain.SessionSearchDoc {
		docs := make([]domain.SessionSearchDoc, n)
		for i := range docs {
			docs[i] = domain.SessionSearchDoc{
				SourceKey: source, DocKey: fmt.Sprint(i), SessionID: fmt.Sprintf("%s-%d", source, i%5),
				Kind: domain.SessionSearchKindUser, CreatedAt: testNow, IndexedAt: testNow,
				Body: fmt.Sprintf("%s alpha beta gamma %d delta %s", filler, i, filler),
			}
		}
		docs[0].Body = "needle zebra " + docs[0].Body
		docs[1].Body = "needle " + docs[1].Body + " needle"
		return docs
	}
	scores := func(churn bool) []float64 {
		s := sqlitetest.MustOpenAt(t, t.TempDir())
		if churn {
			if err := s.UpsertSessionSearchDocs(ctx, corpus("old", 400, "needle")); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteSessionSearchDocsForSource(ctx, "old"); err != nil {
				t.Fatal(err)
			}
		}
		docs := corpus("new", 50, "other")
		if err := s.UpsertSessionSearchDocs(ctx, docs); err != nil {
			t.Fatal(err)
		}
		if churn {
			// A rewritten document replaces its old index row too.
			docs[2].Body = "needle needle needle " + docs[2].Body
			if err := s.UpsertSessionSearchDocs(ctx, docs[2:3]); err != nil {
				t.Fatal(err)
			}
			docs[2].Body = corpus("new", 50, "other")[2].Body
			if err := s.UpsertSessionSearchDocs(ctx, docs[2:3]); err != nil {
				t.Fatal(err)
			}
		}
		hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{
			MatchQuery: matchExpression("needle zebra"), Group: domain.SessionSearchGroupMessage,
		})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]float64, len(hits))
		for i, hit := range hits {
			out[i] = hit.Rank
		}
		return out
	}
	fresh, churned := scores(false), scores(true)
	if fmt.Sprint(fresh) != fmt.Sprint(churned) {
		t.Fatalf("scores after deletes and rewrites %v, want the fresh index's %v", churned, fresh)
	}
}

// The index lives in its own database file, so a long index transaction (a
// rebuild, an FTS5 merge) holds only that file's write lock: app writes and
// searches go on while it runs. In ao.db those waited up to a second.
func TestAppWritesAndSearchesDoNotWaitForTheIndexWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser, Body: "rotate the zzlocked key",
	})

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "search.db"))
	if err != nil {
		t.Fatalf("open search database: %v", err)
	}
	defer func() { _ = raw.Close() }()
	held, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("search connection: %v", err)
	}
	defer func() { _ = held.Close() }()
	if _, err := held.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("take the index write lock: %v", err)
	}
	defer func() { _, _ = held.ExecContext(ctx, `ROLLBACK`) }()

	started := time.Now()
	seedProject(t, s, "written-while-indexing")
	if took := time.Since(started); took > time.Second {
		t.Fatalf("an app write took %v while the index writer held its lock", took)
	}
	parsed := domain.ParseSessionSearchQuery("zzlocked")
	hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: parsed.MatchQuery, HighlightTerms: parsed.HighlightTerms})
	if err != nil || len(hits) != 1 {
		t.Fatalf("search while the index writer held its lock: %d hits, err %v", len(hits), err)
	}
}

// The index is derived data with its own schema version: a file written by
// another version is cleared and rebuilt by the indexer instead of migrated.
func TestTheIndexIsClearedWhenItsSchemaVersionChanges(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser, Body: "zzversioned text",
	})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "search.db"))
	if err != nil {
		t.Fatalf("open search database: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("set version: %v", err)
	}
	_ = raw.Close()

	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if count, err := s.CountSessionSearchDocs(ctx); err != nil || count != 0 {
		t.Fatalf("documents after a version change = %d (err %v), want a cleared index", count, err)
	}
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser, Body: "zzversioned text",
	})
	parsed := domain.ParseSessionSearchQuery("zzversioned")
	if hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: parsed.MatchQuery, HighlightTerms: parsed.HighlightTerms}); err != nil || len(hits) != 1 {
		t.Fatalf("search after the rebuild: %d hits, err %v", len(hits), err)
	}
}

// The index is its own file and can hold sessions its database no longer has
// (an older backup of ao.db restored beside it). Those are recorded as
// deleted, so search drops them at once and the sweep removes their documents.
// A session the database has, and one already recorded, are left alone.
func TestIndexedSessionsMissingFromTheDatabaseAreRecordedAsDeleted(t *testing.T) {
	ctx := context.Background()
	s, conversation := sessionSearchFixture(t)
	present := string(conversation.SessionID)
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:" + present, DocKey: "m1", SessionID: present, Kind: domain.SessionSearchKindUser, Body: "zzpresent words",
	})
	seedSearchDoc(t, s, domain.SessionSearchDoc{
		SourceKey: "chat:gone", DocKey: "m1", SessionID: "gone", Kind: domain.SessionSearchKindUser, Body: "zzgone words",
	})
	// A source with no documents is indexed state too.
	if err := s.UpsertSessionSearchSource(ctx, domain.SessionSearchSource{
		SourceKey: "chat:empty", Kind: domain.SessionSearchSourceChat, SessionID: "empty",
		State: domain.SessionSearchSourceActive, UpdatedAt: testNow,
	}); err != nil {
		t.Fatalf("UpsertSessionSearchSource: %v", err)
	}
	find := func(word string) int {
		t.Helper()
		parsed := domain.ParseSessionSearchQuery(word)
		hits, err := s.SearchSessionSearchDocs(ctx, domain.SessionSearchParams{MatchQuery: parsed.MatchQuery, HighlightTerms: parsed.HighlightTerms})
		if err != nil {
			t.Fatalf("search %s: %v", word, err)
		}
		return len(hits)
	}
	if find("zzgone") != 1 {
		t.Fatal("the fixture must find the missing session's document before it is recorded")
	}

	recorded, err := s.MarkSessionSearchSessionsMissing(ctx)
	if err != nil || recorded != 2 {
		t.Fatalf("recorded %d (err %v), want the 2 sessions the database does not have", recorded, err)
	}
	orphans, err := s.SessionSearchOrphanSessions(ctx, 10)
	if err != nil {
		t.Fatalf("SessionSearchOrphanSessions: %v", err)
	}
	sort.Strings(orphans)
	if strings.Join(orphans, ",") != "empty,gone" {
		t.Fatalf("recorded as deleted = %v, want empty and gone, not the session the database has", orphans)
	}
	if find("zzgone") != 0 || find("zzpresent") != 1 {
		t.Fatalf("hits: gone=%d present=%d, want 0 and 1", find("zzgone"), find("zzpresent"))
	}
	// Already recorded: a second check has nothing to add.
	if again, err := s.MarkSessionSearchSessionsMissing(ctx); err != nil || again != 0 {
		t.Fatalf("a second check recorded %d (err %v), want 0", again, err)
	}
}

// Found in review: the ranking stage kept the best 2,000 matches first and
// dropped the documents of deleted sessions afterwards. More than 2,000 of
// those, each matching better than a live document, took every place the cap
// has: the first page came back empty although the count found the session,
// and nothing fetched it later. They are left out before the cap now.
func TestDocumentsOfDeletedSessionsDoNotFillTheCandidateCap(t *testing.T) {
	ctx := context.Background()
	s, conversation := sessionSearchFixture(t)
	kept := string(conversation.SessionID)
	// More than the cap (sessionSearchCandidateCap, 2,000), in a session the
	// database does not have. One word each: a shorter document scores better
	// than the kept ones below.
	const pending, live, page = 2100, 30, 25
	docs := make([]domain.SessionSearchDoc, 0, pending+live)
	for index := 0; index < pending; index++ {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: "chat:gone", DocKey: fmt.Sprintf("m%d", index), SessionID: "gone",
			Kind: domain.SessionSearchKindUser, MessageID: fmt.Sprintf("gone-%d", index),
			Body: "zzkept", CreatedAt: testNow, IndexedAt: testNow,
		})
	}
	for index := 0; index < live; index++ {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: "chat:" + kept, DocKey: fmt.Sprintf("m%d", index), SessionID: kept,
			Kind: domain.SessionSearchKindUser, MessageID: fmt.Sprintf("kept-%d", index),
			Body:      "zzkept with extra words that make this document longer than the deleted ones",
			CreatedAt: testNow, IndexedAt: testNow,
		})
	}
	for start := 0; start < len(docs); start += 250 {
		if err := s.UpsertSessionSearchDocs(ctx, docs[start:min(start+250, len(docs))]); err != nil {
			t.Fatalf("UpsertSessionSearchDocs: %v", err)
		}
	}
	parsed := domain.ParseSessionSearchQuery("zzkept")
	params := domain.SessionSearchParams{MatchQuery: parsed.MatchQuery, HighlightTerms: parsed.HighlightTerms}
	perMessage := params
	perMessage.Group, perMessage.Limit = domain.SessionSearchGroupMessage, page

	// The fixture's point: while both sessions are results, the stronger
	// documents are the whole first page, so they are what the cap would keep.
	first, err := s.SearchSessionSearchDocs(ctx, perMessage)
	if err != nil || len(first) != page {
		t.Fatalf("before the delete: %d hits (err %v), want a full page", len(first), err)
	}
	for _, hit := range first {
		if hit.SessionID != "gone" {
			t.Fatalf("before the delete the page holds %q; the fixture needs the other session's documents to score better", hit.SessionID)
		}
	}

	if recorded, err := s.MarkSessionSearchSessionsMissing(ctx); err != nil || recorded != 1 {
		t.Fatalf("recorded %d sessions as deleted (err %v), want 1", recorded, err)
	}

	hits, err := s.SearchSessionSearchDocs(ctx, params)
	if err != nil || len(hits) != 1 || hits[0].SessionID != kept {
		t.Fatalf("grouped search: %d hits (err %v), want the surviving session", len(hits), err)
	}
	if count, err := s.CountSessionSearchDocsMatching(ctx, params); err != nil || count != 1 {
		t.Fatalf("grouped count = %d (err %v), want 1", count, err)
	}
	// Every surviving document is reachable, page by page, exactly once.
	seen := map[int64]bool{}
	for offset := 0; offset <= live; offset += page {
		perMessage.Offset = offset
		rows, err := s.SearchSessionSearchDocs(ctx, perMessage)
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		for _, hit := range rows {
			if hit.SessionID != kept || seen[hit.DocID] {
				t.Fatalf("page at %d holds %q document %d (seen before: %v)", offset, hit.SessionID, hit.DocID, seen[hit.DocID])
			}
			seen[hit.DocID] = true
		}
	}
	if len(seen) != live {
		t.Fatalf("pages reached %d documents, want all %d", len(seen), live)
	}
	if count, err := s.CountSessionSearchDocsMatching(ctx, perMessage); err != nil || count != live {
		t.Fatalf("per-message count = %d (err %v), want %d", count, err, live)
	}
}

// search.db indexes one ao.db. Beside a different one (a restored backup, a
// swapped data directory) it is cleared, not left describing sessions that do
// not exist there.
func TestTheIndexIsClearedBesideADifferentAppDatabase(t *testing.T) {
	ctx := context.Background()
	dir, other := t.TempDir(), t.TempDir()
	for _, d := range []string{dir, other} {
		s, err := sqlite.Open(d)
		if err != nil {
			t.Fatalf("Open %s: %v", d, err)
		}
		if d == dir {
			seedSearchDoc(t, s, domain.SessionSearchDoc{
				SourceKey: "chat:a", DocKey: "m1", SessionID: "a", Kind: domain.SessionSearchKindUser, Body: "zzowned text",
			})
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	// Reopening the same pair keeps the index.
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if count, err := s.CountSessionSearchDocs(ctx); err != nil || count != 1 {
		t.Fatalf("documents after reopening the same database = %d (err %v), want 1", count, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	swapped, err := os.ReadFile(filepath.Join(other, "ao.db"))
	if err != nil {
		t.Fatalf("read other database: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(dir, "ao.db"+suffix))
	}
	if err := os.WriteFile(filepath.Join(dir, "ao.db"), swapped, 0o600); err != nil {
		t.Fatalf("swap database: %v", err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open beside the swapped database: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if count, err := s.CountSessionSearchDocs(ctx); err != nil || count != 0 {
		t.Fatalf("documents beside a different ao.db = %d (err %v), want a cleared index", count, err)
	}
}
