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

// The tests in this file each pin a bug the real-data end-to-end run found
// (docs/session-search/ANALYSIS.md §8.7). They run against the real store: the
// controller tests stub the service, which is how all of these got through.

// seedChatSessionAt is seedChatSession with the session's own start time.
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

// userMessageAt is appendUserMessage at a given time.
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

// ids renders sessions' store-assigned ids, which tests compare against.
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

// The palette sends q="" with sort=recent the moment Cmd+K opens, and the
// Search page sends its sort control. Both were overwritten by the parser's
// default, so recents came back empty (total 168, zero rows) and "newest
// first" was ignored.
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
	// A typed operator still wins over the request.
	typed := searchWith(t, f.svc, "deploy sort:relevance", domain.SessionSearchParams{Sort: domain.SessionSearchSortRecent})
	if got := sessionIDs(typed.Results); strings.Join(got, ",") != ids(older, newer) {
		t.Fatalf("typed sort:relevance = %v, want relevance order", got)
	}
}

// Every Chat session's title document was dated by the indexing pass, so a
// session whose NAME matched looked newest under sort:recent and passed any
// after: filter, however old it was.
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

// A pasted PR URL resolved to its session and then fell through to a browse
// that ignored the session filter: every session, newest first. It now lists
// the PR's session first, then the others that mention it; a PR no AO session
// owns still finds the sessions that discuss it.
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
		Store: store, Namer: store, Machine: "test-machine", Now: func() time.Time { return testNow },
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
	unowned := searchWith(t, svc, "https://github.com/other/repo/pull/6553", domain.SessionSearchParams{})
	if listed := sessionIDs(unowned.Results); strings.Join(listed, ",") != ids(tester) {
		t.Fatalf("unowned PR = %v, want the session that mentions pull/6553", listed)
	}
}

// A filter with no words is a browse, and the browse dropped every filter but
// project, harness, dates and status: "path:x", "machine:x" or "!word" alone
// listed every session.
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

	// path: means sessions that changed the file, not ones that printed it.
	if got := sessionIDs(search(t, f.svc, "status path:src/routes").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: with words = %v, want only the session that edited it", got)
	}
	if got := sessionIDs(search(t, f.svc, "path:src/routes").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: alone = %v, want only the session that edited it", got)
	}
	if got := sessionIDs(search(t, f.svc, "path:src/rou").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("path: prefix = %v, want the last part to match as a prefix", got)
	}
	if got := search(t, f.svc, "machine:another-box"); len(got.Results) != 0 || got.Total != 0 {
		t.Fatalf("machine: alone = %v (total %d), want nothing from another machine", sessionIDs(got.Results), got.Total)
	}
	if got := sessionIDs(search(t, f.svc, "machine:test-machine").Results); len(got) != 2 {
		t.Fatalf("machine: this machine = %v, want both sessions", got)
	}
	if got := sessionIDs(search(t, f.svc, "!tree project:proj").Results); strings.Join(got, ",") != ids(editor) {
		t.Fatalf("exclusion browse = %v, want the session without the word", got)
	}
	after := testNow.Add(-time.Hour).Format(time.RFC3339)
	if got := search(t, f.svc, "project:proj after:"+after[:10]); len(got.Results) != 2 {
		t.Fatalf("date browse = %v, want both sessions (created %s)", sessionIDs(got.Results), testNow)
	}
}

// "· N matches" on a row must be the number of stops the find bar then steps
// through. It counted reviewer-chat matches the find bar excludes, left out the
// title the find bar included, and stopped counting at the 2,000-candidate cap.
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
	// The reviewer chat matches too, but the row opens the session's own
	// transcript, where its count and find bar are.
	if row.Results[0].ReviewID != "" || row.Results[0].MessageID == "review-msg" {
		t.Fatalf("row = %+v, want the session's own transcript, not its reviewer chat", row.Results[0])
	}
	stops, err := f.svc.MatchesForSession(ctx, string(session.ID), domain.ParseSessionSearchQuery("gzip").MatchQuery)
	if err != nil {
		t.Fatalf("MatchesForSession: %v", err)
	}
	// Three messages; the command is shown with the first, so it is not a stop
	// of its own; the title and the reviewer chat are not in the transcript.
	if row.Results[0].SessionMatches != 3 || len(stops) != 3 {
		t.Fatalf("row says %d matches, find bar has %d stops; want 3 and 3", row.Results[0].SessionMatches, len(stops))
	}
	for _, stop := range stops {
		if stop.Kind == domain.SessionSearchKindTitle || stop.MessageID == "review-msg" {
			t.Fatalf("find bar stop %+v is not in the transcript", stop)
		}
	}
}

// A long session gathered hundreds of weak matches on common words and beat
// the session named exactly what was typed (the orchestrator, which relays
// every worker's messages, and a 600-message review). The tail now counts a
// handful of a session's other matches.
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
	// The unbounded tail is what let volume win; keep the failure visible. The
	// name tier would now put the named session first on its own, so it is off.
	unbounded := searchWith(t, f.svc, "status endpoint review",
		domain.SessionSearchParams{Ranking: domain.SessionSearchRanking{SessionTailDepth: -1, NameTierOff: true}})
	if got := sessionIDs(unbounded.Results); strings.Join(got, ",") != ids(hub, named) {
		t.Fatalf("unbounded tail = %v; this test no longer reproduces the hub bias it guards against", got)
	}
}

// Found on the MacBook Pro's archive: "amp smoke test" put the
// session named "Amp ChatGPT AO smoke test" 8th, and its exact name put it 3rd.
// Amp transcripts are not indexed, so its name was all it had, and the bounded
// tail still let long sessions mentioning the words hundreds of times win. A
// session named for the query now comes first. Only a name someone gave counts:
// a session whose name is its own id is not "named" for the words in the id.
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
	// Ids are assigned per project, so this one is amp-smoke-test-1, and its
	// name is that id.
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
	// Keep the failure visible: without the tier, volume (or the id) wins.
	off := searchWith(t, f.svc, "amp smoke test",
		domain.SessionSearchParams{Ranking: domain.SessionSearchRanking{NameTierOff: true}})
	if got := sessionIDs(off.Results); len(got) == 0 || got[0] == string(named.ID) {
		t.Fatalf("tier off = %v; this test no longer reproduces the bias it guards against", got)
	}
}

// Tool details are provider JSON. Indexed raw, snippets showed escapes such as
// "<" and a literal "\n", and the keys used up the detail budget.
func TestToolDetailsAreIndexedAsText(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-tool", "conv-tool")
	appendUserMessage(t, f.store, session, "conv-tool", "tool-msg", "tool-turn", "render the workspace")
	addActivity(t, f.store, "conv-tool", "tool-turn", domain.ConversationActivity{
		ID: "tool-1", Kind: domain.ActivityKindMCPTool, Status: domain.ActivityStatusCompleted,
		Detail: []byte("{\"server\":\"ao\",\"tool\":\"html_preview\",\"arguments\":{\"html\":\"\\u003cChatWorkspace quokka /\\u003e\\nnext line\"}}"),
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

// One-character words are dropped; a query of nothing else answers at once
// instead of expanding "c*" over the whole index.
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

// A fresh daemon answered "ready" with nothing indexed until its first pass
// (started 5 s after boot) finished, so Cmd+K showed "no results" with no
// "still indexing" note.
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
