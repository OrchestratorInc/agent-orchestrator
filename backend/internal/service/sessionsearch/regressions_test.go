package sessionsearch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func seedChatSessionAt(t *testing.T, store *sqlite.Store, projectID, sessionID, conversationID string, createdAt time.Time) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: projectID, Path: "/tmp/" + projectID, RegisteredAt: createdAt}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ID: domain.SessionID(sessionID), ProjectID: domain.ProjectID(projectID), Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: createdAt},
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/tmp/" + projectID, ProviderConversationID: "thread-" + sessionID,
			ControllerGeneration: "generation-" + sessionID,
		},
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := store.CreateConversation(ctx, conversationID, domain.ConversationScopeSession,
		domain.ProjectID(projectID), session.ID, createdAt); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return session
}

func userMessageAt(t *testing.T, store *sqlite.Store, session domain.SessionRecord, conversationID, messageID, text string, at time.Time) {
	t.Helper()
	turn := "turn-" + messageID
	created, err := store.AppendUserMessage(context.Background(), conversationID, session.ID, "generation-"+string(session.ID),
		domain.ConversationMessage{ID: messageID, Origin: domain.MessageOriginHuman, Text: text, ClientMessageID: "client-" + messageID},
		turn, at)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage %s: created=%v err=%v", messageID, created, err)
	}
	if err := store.BindTurnToProvider(context.Background(), turn, "provider-"+turn, at); err != nil {
		t.Fatalf("BindTurnToProvider %s: %v", turn, err)
	}
}

func sessionIDs(results []domain.SessionSearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.SessionID)
	}
	return ids
}

func ids(sessions ...domain.SessionRecord) string {
	out := make([]string, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, string(session.ID))
	}
	return strings.Join(out, ",")
}

func searchWith(t *testing.T, svc *sessionsearch.Service, raw string, params domain.SessionSearchParams) domain.SessionSearchResponse {
	t.Helper()
	response, err := svc.Search(context.Background(), domain.ParseSessionSearchQuery(raw), params)
	if err != nil {
		t.Fatalf("Search(%q): %v", raw, err)
	}
	return response
}

func TestSortFromTheRequestIsNotOverwrittenByTheParser(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	older := seedChatSessionAt(t, f.store, "proj", "sess-older", "conv-older", testNow.Add(-48*time.Hour))
	userMessageAt(t, f.store, older, "conv-older", "older-msg", "deploy deploy deploy the gateway", testNow.Add(-48*time.Hour))
	newer := seedChatSessionAt(t, f.store, "proj", "sess-newer", "conv-newer", testNow.Add(-time.Hour))
	userMessageAt(t, f.store, newer, "conv-newer", "newer-msg", "one deploy note", testNow.Add(-time.Hour))
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	recents := searchWith(t, f.svc, "", domain.SessionSearchParams{Sort: domain.SessionSearchSortRecent, Limit: 8})
	if got := sessionIDs(recents.Results); strings.Join(got, ",") != ids(newer, older) {
		t.Fatalf("Cmd+K recents = %v (total %d), want both sessions newest first", got, recents.Total)
	}
	if recents.Sort != domain.SessionSearchSortRecent {
		t.Fatalf("sort = %q, want recent", recents.Sort)
	}

	relevant := searchWith(t, f.svc, "deploy", domain.SessionSearchParams{})
	if got := sessionIDs(relevant.Results); strings.Join(got, ",") != ids(older, newer) {
		t.Fatalf("relevance order = %v, want the session that says deploy three times first", got)
	}
	newest := searchWith(t, f.svc, "deploy", domain.SessionSearchParams{Sort: domain.SessionSearchSortRecent})
	if got := sessionIDs(newest.Results); strings.Join(got, ",") != ids(newer, older) || newest.Sort != domain.SessionSearchSortRecent {
		t.Fatalf("newest-first order = %v (sort %q), want the newer session first", got, newest.Sort)
	}
	typed := searchWith(t, f.svc, "deploy sort:relevance", domain.SessionSearchParams{Sort: domain.SessionSearchSortRecent})
	if got := sessionIDs(typed.Results); strings.Join(got, ",") != ids(older, newer) {
		t.Fatalf("typed sort:relevance = %v, want relevance order", got)
	}
}

func TestTitleDocumentsAreDatedByTheirSession(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	old := seedChatSessionAt(t, f.store, "proj", "sess-old", "conv-old", testNow.Add(-30*24*time.Hour))
	renameForSearch(t, f, old, "Quokka migration review")
	userMessageAt(t, f.store, old, "conv-old", "old-msg", "an unrelated note", testNow.Add(-30*24*time.Hour))
	recent := seedChatSessionAt(t, f.store, "proj", "sess-recent", "conv-recent", testNow.Add(-2*time.Hour))
	userMessageAt(t, f.store, recent, "conv-recent", "recent-msg", "the quokka migration landed", testNow.Add(-2*time.Hour))
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	if got := sessionIDs(search(t, f.svc, "quokka sort:recent").Results); strings.Join(got, ",") != ids(recent, old) {
		t.Fatalf("sort:recent = %v, want the session active two hours ago before the one named a month ago", got)
	}
	after := testNow.Add(-24 * time.Hour).Format("2006-01-02")
	if got := sessionIDs(search(t, f.svc, "quokka after:"+after).Results); strings.Join(got, ",") != ids(recent) {
		t.Fatalf("after:%s = %v, want only the recent session; the old one matches by name only", after, got)
	}
}

type fakePRs map[string]string

func (p fakePRs) SessionSearchSessionForPR(_ context.Context, url string) (string, bool, error) {
	session, ok := p[url]
	return session, ok, nil
}

func TestPastedPRURLListsItsSessionFirstThenMentions(t *testing.T) {
	store := newFixture(t, sessionsearch.TranscriptRoots{}).store
	const url = "https://github.com/acme/widgets/pull/6553"
	owner := seedChatSession(t, store, "proj", "sess-owner", "conv-owner")
	appendUserMessage(t, store, owner, "conv-owner", "owner-msg", "owner-turn", "implement the widget cache")
	tester := seedChatSession(t, store, "proj", "sess-tester", "conv-tester")
	appendUserMessage(t, store, tester, "conv-tester", "tester-msg", "tester-turn", "runtime-validate "+url+" at its head")
	other := seedChatSession(t, store, "proj", "sess-other", "conv-other")
	appendUserMessage(t, store, other, "conv-other", "other-msg", "other-turn", "unrelated work on pull requests")
	svc := sessionsearch.New(sessionsearch.Options{
		Store: store, Namer: store, Now: func() time.Time { return testNow },
		PRs: fakePRs{url: string(owner.ID)},
	})
	if _, err := svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	got := searchWith(t, svc, url, domain.SessionSearchParams{Limit: 8})
	if listed := sessionIDs(got.Results); strings.Join(listed, ",") != ids(owner, tester) || got.Total != 2 {
		t.Fatalf("pasted PR URL = %v (total %d), want its session then the one mentioning it, and nothing else", listed, got.Total)
	}
	unknown := searchWith(t, svc, "https://github.com/acme/widgets/pull/6553/files", domain.SessionSearchParams{})
	if len(unknown.Results) == 0 {
		t.Fatal("a PR URL with a trailing path segment must still be recognized")
	}
	otherRepo := searchWith(t, svc, "https://github.com/other/repo/pull/6553", domain.SessionSearchParams{})
	if len(otherRepo.Results) != 0 {
		t.Fatalf("other repository's PR = %v, want nothing: only acme/widgets/pull/6553 was mentioned", sessionIDs(otherRepo.Results))
	}
	unowned := sessionsearch.New(sessionsearch.Options{Store: store, Namer: store, Now: func() time.Time { return testNow }, PRs: fakePRs{}})
	if listed := sessionIDs(searchWith(t, unowned, url, domain.SessionSearchParams{}).Results); strings.Join(listed, ",") != ids(tester) {
		t.Fatalf("unowned PR = %v, want the session that mentions it", listed)
	}
}

func TestBrowseAppliesEveryFilter(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	editor := seedChatSession(t, f.store, "proj", "sess-editor", "conv-editor")
	appendUserMessage(t, f.store, editor, "conv-editor", "editor-msg", "editor-turn", "add the status route")
	addActivity(t, f.store, "conv-editor", "editor-turn", domain.ConversationActivity{
		ID: "editor-change", Kind: domain.ActivityKindFileChange, Status: domain.ActivityStatusCompleted,
		Detail: []byte(`{"files":[{"path":"src/routes/status.js"}]}`),
	})
	lister := seedChatSession(t, f.store, "proj", "sess-lister", "conv-lister")
	appendUserMessage(t, f.store, lister, "conv-lister", "lister-msg", "lister-turn", "what is in the tree")
	addCommandActivity(t, f.store, "conv-lister", "lister-turn", "lister-ls", "ls src",
		"src/routes src/lib src/routes/status.js", domain.ActivityStatusCompleted)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	if got := sessionIDs(search(t, f.svc, "status path:src/routes").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: with words = %v, want only the session that edited it", got)
	}
	if got := sessionIDs(search(t, f.svc, "path:src/routes").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: alone = %v, want only the session that edited it", got)
	}
	if got := sessionIDs(search(t, f.svc, "path:src/rou").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: prefix = %v, want the last part to match as a prefix", got)
	}
	if got := sessionIDs(search(t, f.svc, "!tree project:proj").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("exclusion browse = %v, want the session without the word", got)
	}
	after := testNow.Add(-time.Hour).Format(time.RFC3339)
	if got := search(t, f.svc, "project:proj after:"+after[:10]); len(got.Results) != 2 {
		t.Fatalf("date browse = %v, want both sessions (created %s)", sessionIDs(got.Results), testNow)
	}
}

func TestRowMatchCountEqualsFindBarStops(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-count", "conv-count")
	renameForSearch(t, f, session, "Gzip negotiation")
	for i := range 3 {
		appendUserMessage(t, f.store, session, "conv-count", fmt.Sprintf("msg-%d", i), fmt.Sprintf("turn-%d", i),
			fmt.Sprintf("gzip negotiation step %d", i))
	}
	addCommandActivity(t, f.store, "conv-count", "turn-0", "cmd-0", "curl -H 'Accept-Encoding: gzip'",
		"content-encoding: gzip", domain.ActivityStatusCompleted)
	review := domain.Review{ID: "review-count", SessionID: session.ID, ProjectID: session.ProjectID,
		Harness: domain.ReviewerCodex, CreatedAt: testNow, UpdatedAt: testNow}
	if err := f.store.UpsertReview(ctx, review); err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}
	conversation, err := f.store.CreateReviewConversation(ctx, "conv-count-review", review.ID, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatalf("CreateReviewConversation: %v", err)
	}
	if claimed, err := f.store.ClaimReviewChatController(ctx, review.ID, "review-provider", "review-generation", testNow); err != nil || !claimed {
		t.Fatalf("ClaimReviewChatController: claimed=%v err=%v", claimed, err)
	}
	if created, err := f.store.AppendReviewUserMessage(ctx, conversation.ID, session.ID, review.ID, "review-generation",
		domain.ConversationMessage{ID: "review-msg", Text: "gzip looks fine", Origin: domain.MessageOriginHuman},
		"review-turn", testNow); err != nil || !created {
		t.Fatalf("AppendReviewUserMessage: created=%v err=%v", created, err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	row := search(t, f.svc, "gzip")
	if len(row.Results) != 1 {
		t.Fatalf("results = %+v", row.Results)
	}
	if row.Results[0].ReviewID != "" || row.Results[0].MessageID == "review-msg" {
		t.Fatalf("row = %+v, want the session's own transcript, not its reviewer chat", row.Results[0])
	}
	if row.Results[0].SessionMatches != 3 {
		t.Fatalf("row says %d matches, want 3", row.Results[0].SessionMatches)
	}
}

func TestALongSessionDoesNotOutrankTheOneNamedForTheQuery(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	named := seedChatSession(t, f.store, "proj", "sess-named", "conv-named")
	renameForSearch(t, f, named, "Status endpoint review")
	appendUserMessage(t, f.store, named, "conv-named", "named-msg", "named-turn", "please look at the status route draft")
	hub := seedChatSession(t, f.store, "proj", "sess-hub", "conv-hub")
	renameForSearch(t, f, hub, "Orchestrator")
	words := []string{"status", "review", "endpoint"}
	for i := range 120 {
		appendUserMessage(t, f.store, hub, "conv-hub", fmt.Sprintf("hub-%d", i), fmt.Sprintf("hub-turn-%d", i),
			fmt.Sprintf("worker %d reports %s on its queue item and waits for the next grant", i, words[i%len(words)]))
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	if got := sessionIDs(search(t, f.svc, "status endpoint review").Results); strings.Join(got, ",") != ids(named, hub) {
		t.Fatalf("results = %v, want the session named for the query first", got)
	}
}

func TestASessionNamedForTheQueryLeadsEvenWithNothingButItsName(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	named := seedChatSession(t, f.store, "proj", "sess-amp", "conv-amp")
	renameForSearch(t, f, named, "Amp ChatGPT AO smoke test")
	busy := seedChatSession(t, f.store, "proj", "sess-busy", "conv-busy")
	renameForSearch(t, f, busy, "Diagnose update blocker")
	for i := range 60 {
		appendUserMessage(t, f.store, busy, "conv-busy", fmt.Sprintf("busy-%d", i), fmt.Sprintf("busy-turn-%d", i),
			fmt.Sprintf("fresh amp tui smoke session %d: the smoke test passed", i))
	}
	idNamed := seedChatSession(t, f.store, "amp-smoke-test", "unused", "conv-id")
	renameForSearch(t, f, idNamed, string(idNamed.ID))
	appendUserMessage(t, f.store, idNamed, "conv-id", "id-msg", "id-turn", "check the release notes")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	for _, query := range []string{"amp smoke test", "Amp ChatGPT AO smoke test"} {
		got := sessionIDs(search(t, f.svc, query).Results)
		if len(got) == 0 || got[0] != string(named.ID) {
			t.Fatalf("%q = %v, want the session named for the query first", query, got)
		}
	}
}

func TestADeletedSessionLeavesTheIndex(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-gone", "conv-gone")
	appendUserMessage(t, f.store, session, "conv-gone", "gone-msg", "gone-turn", "rotate the hmac secret before friday")
	kept := seedChatSession(t, f.store, "proj", "sess-kept", "conv-kept")
	appendUserMessage(t, f.store, kept, "conv-kept", "kept-msg", "kept-turn", "rotate the logging key")
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := sessionIDs(search(t, f.svc, "hmac").Results); len(got) != 1 || got[0] != string(session.ID) {
		t.Fatalf("before delete: hmac = %v, want the session", got)
	}

	session.IsTerminated = true
	if err := f.store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if deleted, err := f.store.DeleteTerminatedSession(ctx, session.ID); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}

	if got := search(t, f.svc, "hmac").Results; len(got) != 0 {
		t.Fatalf("after delete: hmac = %v, want nothing from a deleted session", sessionIDs(got))
	}
	if got := sessionIDs(search(t, f.svc, "rotate").Results); len(got) != 1 || got[0] != string(kept.ID) {
		t.Fatalf("after delete: rotate = %v, want only the kept session", got)
	}
	coverage, err := f.svc.Coverage(ctx)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if want := int64(1); coverage.IndexedSources != want {
		t.Fatalf("indexed sources = %d, want %d: the deleted session's source must go too", coverage.IndexedSources, want)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if count, err := f.store.CountSessionSearchDocsForSource(ctx, sessionsearch.ChatSourceKey(string(session.ID))); err != nil || count != 0 {
		t.Fatalf("documents left for the deleted session = %d (err %v)", count, err)
	}
	if orphans, err := f.store.SessionSearchOrphanSessions(ctx, 10); err != nil || len(orphans) != 0 {
		t.Fatalf("deleted sessions still pending cleanup = %v (err %v)", orphans, err)
	}
}

func TestToolDetailsAreIndexedAsText(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-tool", "conv-tool")
	appendUserMessage(t, f.store, session, "conv-tool", "tool-msg", "tool-turn", "render the workspace")
	addActivity(t, f.store, "conv-tool", "tool-turn", domain.ConversationActivity{
		ID: "tool-1", Kind: domain.ActivityKindMCPTool, Status: domain.ActivityStatusCompleted,
		Detail: []byte("{\"server\":\"ao\",\"tool\":\"html_preview\",\"arguments\":{\"query\":\"\\u003cChatWorkspace quokka /\\u003e\\nnext line\"}}"),
	})
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := searchWith(t, f.svc, "quokka", domain.SessionSearchParams{Group: domain.SessionSearchGroupMessage})
	var snippet string
	for _, result := range results.Results {
		if result.Kind == domain.SessionSearchKindTool {
			snippet = result.Snippet
		}
	}
	if !strings.Contains(snippet, "<ChatWorkspace") {
		t.Fatalf("tool snippet = %q, want the decoded text", snippet)
	}
	for _, raw := range []string{"\\u003c", "\\n", "\"arguments\""} {
		if strings.Contains(snippet, raw) {
			t.Fatalf("tool snippet = %q still shows raw JSON (%s)", snippet, raw)
		}
	}
}

func TestOneCharacterQueriesAnswerWithoutAPrefixScan(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-c", "conv-c")
	appendUserMessage(t, f.store, session, "conv-c", "c-msg", "c-turn", "compile the C++ code with clang")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "C++"); len(got.Results) != 0 {
		t.Fatalf("C++ = %v, want no prefix match on c", sessionIDs(got.Results))
	}
	if got := sessionIDs(search(t, f.svc, "C++ clang").Results); strings.Join(got, ",") != ids(session) {
		t.Fatalf("C++ clang = %v, want the session found by its real word", got)
	}
}

func TestCoverageIsBackfillingUntilTheFirstPassCompletes(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-fresh", "conv-fresh")
	appendUserMessage(t, f.store, session, "conv-fresh", "fresh-msg", "fresh-turn", "fresh content")
	before, err := f.svc.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if before.State != "backfilling" {
		t.Fatalf("state before the first pass = %q, want backfilling", before.State)
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	after, err := f.svc.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if after.State != "ready" || after.IndexedDocs == 0 {
		t.Fatalf("after the first pass = %+v, want ready with documents", after)
	}
}

func TestTheWordAsTypedOutranksItsWidenedForms(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	busy := seedChatSession(t, f.store, "proj", "sess-busy", "conv-busy")
	for i := range 40 {
		appendUserMessage(t, f.store, busy, "conv-busy", fmt.Sprintf("busy-%d", i), fmt.Sprintf("busy-turn-%d", i),
			fmt.Sprintf("rebased worker %d onto main and rebased again after review", i))
	}
	exact := seedChatSession(t, f.store, "proj", "sess-exact", "conv-exact")
	appendUserMessage(t, f.store, exact, "conv-exact", "exact-widened", "exact-turn-1", "rebased it, rebased it again, then rebased once more")
	appendUserMessage(t, f.store, exact, "conv-exact", "exact-typed", "exact-turn-2", "we were rebasing the release branch when it broke")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	got := search(t, f.svc, "rebasing")
	if strings.Join(sessionIDs(got.Results), ",") != ids(exact, busy) {
		t.Fatalf("results = %s, want the session holding the word as typed first", describe(got.Results))
	}
	if got.Results[0].MessageID != "exact-typed" {
		t.Fatalf("top row = %q, want the message that says \"rebasing\"", got.Results[0].MessageID)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results = %s, want the widened session still listed", describe(got.Results))
	}
}

func TestSweepDropsARolledBackTurn(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-rollback", "conv-rollback")
	appendUserMessage(t, f.store, session, "conv-rollback", "keep-msg", "turn-keep", "keep zzKeepNeedle in mind")
	settleAssistantMessage(t, f.store, "conv-rollback", "keep-reply", "turn-keep", "noted")
	appendUserMessage(t, f.store, session, "conv-rollback", "drop-msg", "turn-drop", "try zzDropNeedle instead")
	addCommandActivity(t, f.store, "conv-rollback", "turn-drop", "cmd-drop", "go test ./...", "FAIL zzDropOutput", domain.ActivityStatusCompleted)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if len(search(t, f.svc, "zzDropNeedle").Results) != 1 || len(search(t, f.svc, "zzDropOutput").Results) != 1 {
		t.Fatalf("the turn was not indexed before the rollback")
	}

	if _, err := f.store.RollbackTurns(ctx, "conv-rollback", "turn-drop", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RollbackTurns: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for _, word := range []string{"zzDropNeedle", "zzDropOutput"} {
		if got := search(t, f.svc, word).Results; len(got) != 0 {
			t.Fatalf("%s still searchable after a rollback: %s", word, describe(got))
		}
	}
	if len(search(t, f.svc, "zzKeepNeedle").Results) != 1 {
		t.Fatalf("the kept turn went missing")
	}
}

func TestReindexSkipsActivitiesOfARolledBackTurn(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-undone", "conv-undone")
	appendUserMessage(t, f.store, session, "conv-undone", "keep-msg", "turn-keep", "the kept turn")
	settleAssistantMessage(t, f.store, "conv-undone", "keep-reply", "turn-keep", "done")
	appendUserMessage(t, f.store, session, "conv-undone", "drop-msg", "turn-drop", "the undone turn")
	addCommandActivity(t, f.store, "conv-undone", "turn-drop", "cmd-drop", "make", "error zzUndoneOutput", domain.ActivityStatusCompleted)
	addActivity(t, f.store, "conv-undone", "turn-drop", domain.ConversationActivity{
		ID: "files-drop", Kind: domain.ActivityKindFileChange, Status: domain.ActivityStatusCompleted,
		Detail: []byte(`{"files":[{"path":"src/zzundone/file.go"}]}`),
	})
	if _, err := f.store.RollbackTurns(ctx, "conv-undone", "turn-drop", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RollbackTurns: %v", err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	for _, query := range []string{"zzUndoneOutput", "path:zzundone"} {
		if got := search(t, f.svc, query).Results; len(got) != 0 {
			t.Fatalf("%s matched a rolled-back turn: %s", query, describe(got))
		}
	}
}

func TestSweepReindexesAnEqualLengthEdit(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-queued", "conv-queued")
	created, err := f.store.AppendUserMessage(ctx, "conv-queued", session.ID, "generation-"+string(session.ID),
		domain.ConversationMessage{ID: "queued-msg", Origin: domain.MessageOriginHuman, Text: "deploy zzalpha tonight", ClientMessageID: "client-queued"},
		"turn-queued", testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if len(search(t, f.svc, "zzalpha").Results) != 1 {
		t.Fatalf("the queued prompt was not indexed")
	}
	if err := f.store.UpdateQueuedTurnMessage(ctx, "conv-queued", "turn-queued", "deploy zzomega tonight", "", 0,
		testNow.Add(time.Minute), domain.ConversationQueuedEditDelivery{}); err != nil {
		t.Fatalf("UpdateQueuedTurnMessage: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzalpha").Results; len(got) != 0 {
		t.Fatalf("the edited-away word is still searchable: %s", describe(got))
	}
	if len(search(t, f.svc, "zzomega").Results) != 1 {
		t.Fatalf("the edited prompt was not re-indexed")
	}
}

func TestSweepPicksUpARename(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-rename", "conv-rename")
	renameForSearch(t, f, session, "Zzoldname investigation")
	appendUserMessage(t, f.store, session, "conv-rename", "rename-msg", "turn-rename", "look at the cache")
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if _, err := f.store.RenameSession(ctx, session.ID, "Zznewname investigation", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zznewname").Results; len(got) == 0 || got[0].SessionID != string(session.ID) {
		t.Fatalf("the new name is not searchable: %s", describe(got))
	}
	if got := search(t, f.svc, "zzoldname").Results; len(got) != 0 {
		t.Fatalf("the old name is still searchable: %s", describe(got))
	}
}

func TestSweepReindexesAnEditInTheSameSecond(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-same-second", "conv-same-second")
	created, err := f.store.AppendUserMessage(ctx, "conv-same-second", session.ID, "generation-"+string(session.ID),
		domain.ConversationMessage{ID: "queued-msg", Origin: domain.MessageOriginHuman, Text: "deploy zzfirst tonight", ClientMessageID: "client-queued"},
		"turn-queued", testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if err := f.store.UpdateQueuedTurnMessage(ctx, "conv-same-second", "turn-queued", "deploy zzsecnd tonight", "", 0,
		testNow.Add(100*time.Millisecond), domain.ConversationQueuedEditDelivery{}); err != nil {
		t.Fatalf("UpdateQueuedTurnMessage: %v", err)
	}
	if _, err := f.svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := search(t, f.svc, "zzfirst").Results; len(got) != 0 {
		t.Fatalf("the edited-away word is still searchable: %s", describe(got))
	}
	if len(search(t, f.svc, "zzsecnd").Results) != 1 {
		t.Fatalf("an edit in the same second was not re-indexed")
	}
}
