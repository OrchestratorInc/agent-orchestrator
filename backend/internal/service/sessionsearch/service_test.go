package sessionsearch_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var testNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

type fixture struct {
	store *sqlite.Store
	svc   *sessionsearch.Service
}

func newFixture(t *testing.T, roots sessionsearch.TranscriptRoots) fixture {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	svc := sessionsearch.New(sessionsearch.Options{
		Store:   store,
		Roots:   roots,
		Machine: "test-machine",
		// The daemon wires the store as the namer, so names and session state
		// resolve in production. The fixture must do the same or a test can pass
		// while the shipped surface shows an empty state.
		Namer: store,
		Now:   func() time.Time { return testNow },
	})
	return fixture{store: store, svc: svc}
}

// seedChatSession creates a Chat session with one conversation and returns it.
func seedChatSession(t *testing.T, store *sqlite.Store, projectID, sessionID, conversationID string) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: projectID, Path: "/tmp/" + projectID,
		RegisteredAt: testNow,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ID:        domain.SessionID(sessionID),
		ProjectID: domain.ProjectID(projectID),
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: testNow},
		Metadata: domain.SessionMetadata{
			Branch:                 "feat/search",
			WorkspacePath:          "/tmp/" + projectID,
			ProviderConversationID: "thread-" + sessionID,
			ControllerGeneration:   "generation-" + sessionID,
		},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := store.CreateConversation(ctx, conversationID, domain.ConversationScopeSession,
		domain.ProjectID(projectID), session.ID, testNow); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return session
}

func appendUserMessage(t *testing.T, store *sqlite.Store, session domain.SessionRecord, conversationID, messageID, turnID, text string) {
	t.Helper()
	ctx := context.Background()
	created, err := store.AppendUserMessage(ctx, conversationID, session.ID, "generation-"+string(session.ID),
		domain.ConversationMessage{
			ID: messageID, Origin: domain.MessageOriginHuman, Text: text,
			ClientMessageID: "client-" + messageID,
		}, turnID, testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage %s: created=%v err=%v", messageID, created, err)
	}
	if err := store.BindTurnToProvider(ctx, turnID, "provider-"+turnID, testNow); err != nil {
		t.Fatalf("BindTurnToProvider %s: %v", turnID, err)
	}
}

func settleAssistantMessage(t *testing.T, store *sqlite.Store, conversationID, messageID, turnID, text string) {
	t.Helper()
	if err := store.SettleAssistantMessage(context.Background(), conversationID, messageID,
		"provider-"+turnID, text, "assistant-"+messageID, testNow); err != nil {
		t.Fatalf("SettleAssistantMessage %s: %v", messageID, err)
	}
}

func addActivity(t *testing.T, store *sqlite.Store, conversationID, turnID string, activity domain.ConversationActivity) {
	t.Helper()
	if err := store.UpsertActivity(context.Background(), conversationID, "provider-"+turnID, activity, testNow); err != nil {
		t.Fatalf("UpsertActivity %s: %v", activity.ID, err)
	}
}

// addCommandActivity records a command the way the Chat controller does: the row
// first, then its streamed output through the append path that enforces AO's
// per-row cap. Command output is never written through UpsertActivity.
func addCommandActivity(t *testing.T, store *sqlite.Store, conversationID, turnID, activityID, summary, output string, status domain.ActivityStatus) {
	t.Helper()
	ctx := context.Background()
	providerItemID := "item-" + activityID
	addActivity(t, store, conversationID, turnID, domain.ConversationActivity{
		ID:             activityID,
		Kind:           domain.ActivityKindCommand,
		Status:         domain.ActivityStatusRunning,
		Summary:        summary,
		ProviderItemID: providerItemID,
	})
	if output != "" {
		if _, err := store.AppendCommandOutput(ctx, conversationID, providerItemID, output, testNow); err != nil {
			t.Fatalf("AppendCommandOutput %s: %v", activityID, err)
		}
	}
	if _, err := store.SettleActivityStreamedText(ctx, conversationID, providerItemID, summary, testNow); err != nil {
		// Settling is best-effort for this fixture; the row and its output are
		// what the index reads.
		t.Logf("settle %s: %v", activityID, err)
	}
	_ = status
}

// addReasoningActivity records a reasoning activity with its streamed summary.
func addReasoningActivity(t *testing.T, store *sqlite.Store, conversationID, turnID, activityID, text string) {
	t.Helper()
	ctx := context.Background()
	providerItemID := "item-" + activityID
	addActivity(t, store, conversationID, turnID, domain.ConversationActivity{
		ID:             activityID,
		Kind:           domain.ActivityKindReasoning,
		Status:         domain.ActivityStatusRunning,
		ProviderItemID: providerItemID,
	})
	if _, err := store.AppendActivityStreamedText(ctx, conversationID, providerItemID, text, testNow); err != nil {
		t.Fatalf("AppendActivityStreamedText %s: %v", activityID, err)
	}
}

func conversationFor(t *testing.T, store *sqlite.Store, sessionID string) domain.SessionSearchConversation {
	t.Helper()
	conversations, err := store.ListSessionSearchConversations(context.Background())
	if err != nil {
		t.Fatalf("ListSessionSearchConversations: %v", err)
	}
	for _, conversation := range conversations {
		if string(conversation.SessionID) == sessionID {
			return conversation
		}
	}
	t.Fatalf("no conversation for session %s", sessionID)
	return domain.SessionSearchConversation{}
}

// search runs a query through the same parser the UI and CLI use.
func search(t *testing.T, svc *sessionsearch.Service, raw string) domain.SessionSearchResponse {
	t.Helper()
	parsed := domain.ParseSessionSearchQuery(raw)
	response, err := svc.Search(context.Background(), parsed, domain.SessionSearchParams{})
	if err != nil {
		t.Fatalf("Search(%q): %v", raw, err)
	}
	return response
}

func TestIndexChatIndexesMessagesAndIsIncremental(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "the login test is flaky")
	settleAssistantMessage(t, f.store, "conv-1", "msg-1-assistant", "turn-1", "I awaited the promise in the login test")

	written, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID)))
	if err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	if written < 2 {
		t.Fatalf("written = %d, want the user and assistant messages", written)
	}
	results := search(t, f.svc, "flaky")
	if len(results.Results) != 1 || results.Results[0].MessageID != "msg-1" {
		t.Fatalf("results = %+v, want the user message with its timeline anchor", results.Results)
	}
	if results.Results[0].SessionID != string(session.ID) {
		t.Fatalf("session = %q, want %q", results.Results[0].SessionID, session.ID)
	}

	// A second pass with nothing new writes nothing: the fingerprint did not move.
	again, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID)))
	if err != nil {
		t.Fatalf("second IndexChat: %v", err)
	}
	if again != 0 {
		t.Fatalf("unchanged pass wrote %d docs, want 0", again)
	}

	// A new turn is picked up, and the earlier documents survive.
	appendUserMessage(t, f.store, session, "conv-1", "msg-2", "turn-2", "also check the logout redirect")
	settleAssistantMessage(t, f.store, "conv-1", "msg-2-assistant", "turn-2", "the logout redirect now returns to the login page")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("incremental IndexChat: %v", err)
	}
	if got := search(t, f.svc, "flaky"); len(got.Results) != 1 {
		t.Fatalf("earlier message lost after incremental pass: %+v", got.Results)
	}
	if got := search(t, f.svc, "logout"); len(got.Results) == 0 {
		t.Fatalf("new message not indexed")
	}
}

// The sweep skips a conversation whose aggregate stamp is unchanged. That skip
// is the reason an idle sweep stays cheap, and it is also the failure mode that
// would silently freeze the index, so it is pinned from both sides: nothing is
// written when nothing changed, and both a new message and a new activity are
// still picked up.
func TestSweepStampSkipsUnchangedButDetectsNewWork(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-stamp", "conv-stamp")
	appendUserMessage(t, f.store, session, "conv-stamp", "msg-1", "turn-1", "first message")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	if got := search(t, f.svc, "first"); len(got.Results) != 1 {
		t.Fatalf("first: %+v", got.Results)
	}

	unchanged, err := f.svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if unchanged != 0 {
		t.Fatalf("an unchanged sweep wrote %d documents, want 0", unchanged)
	}

	appendUserMessage(t, f.store, session, "conv-stamp", "msg-2", "turn-2", "second message about zebras")
	if _, err := f.svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep after message: %v", err)
	}
	if got := search(t, f.svc, "zebras"); len(got.Results) != 1 {
		t.Fatalf("new message not indexed after sweep: %+v", got.Results)
	}

	// An activity is a separate timeline table, so the stamp must include it or a
	// session that only ran commands would never re-index.
	addCommandActivity(t, f.store, "conv-stamp", "turn-2", "cmd-zebra", "go test ./zebra", "zebra output", domain.ActivityStatusCompleted)
	if _, err := f.svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep after activity: %v", err)
	}
	if got := search(t, f.svc, "zebra"); len(got.Results) == 0 {
		t.Fatalf("new activity not indexed after sweep")
	}
}

func TestIndexChatDropsRolledBackContent(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "keep this direction")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	if got := search(t, f.svc, "direction"); len(got.Results) != 1 {
		t.Fatalf("first message missing: %+v", got.Results)
	}
	if _, err := f.store.RollbackTurns(context.Background(), "conv-1", "turn-1", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RollbackTurns: %v", err)
	}
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat after rollback: %v", err)
	}
	if got := search(t, f.svc, "direction"); len(got.Results) != 0 {
		t.Fatalf("rolled-back message still searchable: %+v", got.Results)
	}
}

func TestIndexChatIndexesTouchedPaths(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "fix the login test")
	addActivity(t, f.store, "conv-1", "turn-1", domain.ConversationActivity{
		ID:     "activity-1",
		Kind:   domain.ActivityKindFileChange,
		Status: domain.ActivityStatusCompleted,
		Detail: []byte(`{"files":[{"path":"src/auth/login_test.ts"}]}`),
	})
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	results := search(t, f.svc, "login_test.ts")
	if len(results.Results) != 1 {
		t.Fatalf("path results = %+v, want one", results.Results)
	}
	if results.Results[0].Kind != domain.SessionSearchKindPath {
		t.Fatalf("kind = %q, want path", results.Results[0].Kind)
	}
}

// Command output is the largest body of searchable text in a real database and
// the one every single-harness competitor skips.
func TestIndexChatIndexesCommandOutput(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "run the tests")
	addCommandActivity(t, f.store, "conv-1", "turn-1", "activity-cmd", "go test ./...",
		"--- FAIL: TestSessionSearchRanking (0.02s)\n  expected title to outrank body\n", domain.ActivityStatusFailed)
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	results := search(t, f.svc, "TestSessionSearchRanking")
	if len(results.Results) != 1 {
		t.Fatalf("command output results = %+v, want the failing test name", results.Results)
	}
	if results.Results[0].Kind != domain.SessionSearchKindCommand {
		t.Fatalf("kind = %q, want command", results.Results[0].Kind)
	}
	// The command is shown with the prompt that ran it, so the result opens
	// there: a tool-output match has somewhere to scroll to, like a message.
	if results.Results[0].MessageID != "msg-1" {
		t.Fatalf("command result anchor = %q, want its turn's message msg-1", results.Results[0].MessageID)
	}
}

// A long command output keeps its END: a failure is diagnosed from the tail.
func TestCommandOutputKeepsTheTail(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "run the build")
	// AO caps one command's output at 64 KiB; this fixture stays inside that cap
	// and longer than the index's own per-document budget, so the test proves the
	// index keeps the tail rather than the head.
	padding := make([]byte, 16<<10)
	for index := range padding {
		padding[index] = 'x'
	}
	addCommandActivity(t, f.store, "conv-1", "turn-1", "activity-cmd", "make build",
		string(padding)+"\nlinker error: undefined symbol zzNeedle", domain.ActivityStatusFailed)
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	if got := search(t, f.svc, "zzNeedle"); len(got.Results) != 1 {
		t.Fatalf("tail of a long command output missing: %+v", got.Results)
	}
}

// A file change carries the actual diff. Someone searching for a symbol they
// remember editing must find the changed line, not only the file name.
func TestIndexChatIndexesDiffText(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "add the retry guard")
	addActivity(t, f.store, "conv-1", "turn-1", domain.ConversationActivity{
		ID:      "activity-diff",
		Kind:    domain.ActivityKindFileChange,
		Status:  domain.ActivityStatusCompleted,
		Summary: "Modified src/auth/login.ts",
		Detail: []byte(`{"files":[{"path":"src/auth/login.ts","status":"modified",` +
			`"patch":"@@ -10,3 +10,4 @@\n-const retry = false;\n+const retry = true;\n+function handleSubmit() {}\n"}]}`),
	})
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}

	// A word that appears only inside the added diff line.
	added := search(t, f.svc, "handleSubmit")
	if len(added.Results) == 0 {
		t.Fatal("a symbol added by the diff is not searchable")
	}
	if added.Results[0].Kind != domain.SessionSearchKindPath {
		t.Fatalf("kind = %q, want path for a file change", added.Results[0].Kind)
	}
	// A word that appears only inside the removed diff line.
	removed := search(t, f.svc, "false")
	if len(removed.Results) == 0 {
		t.Fatal("a symbol removed by the diff is not searchable")
	}
	// The path still matches on its own.
	if got := search(t, f.svc, "login.ts"); len(got.Results) == 0 {
		t.Fatal("the touched path is not searchable")
	}
	// Diff context lines and the "+++"/"---" headers must not be indexed as if
	// they were changed code.
	if got := search(t, f.svc, "context"); len(got.Results) != 0 {
		t.Fatalf("a context line was indexed: %+v", got.Results)
	}
}

func TestIndexChatIndexesToolAndReasoningActivities(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "look into the retry loop")
	addActivity(t, f.store, "conv-1", "turn-1", domain.ConversationActivity{
		ID:      "activity-tool",
		Kind:    domain.ActivityKindMCPTool,
		Status:  domain.ActivityStatusCompleted,
		Summary: "mcp__linear__search_issues",
		Detail:  []byte(`{"query":"flaky upload timeout"}`),
	})
	addReasoningActivity(t, f.store, "conv-1", "turn-1", "activity-reasoning",
		"The retry loop never resets its backoff counter.")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	tool := search(t, f.svc, "flaky upload timeout")
	if len(tool.Results) != 1 || tool.Results[0].Kind != domain.SessionSearchKindTool {
		t.Fatalf("tool args results = %+v", tool.Results)
	}
	reasoning := search(t, f.svc, "backoff counter")
	if len(reasoning.Results) != 1 || reasoning.Results[0].Kind != domain.SessionSearchKindAssistant {
		t.Fatalf("reasoning results = %+v", reasoning.Results)
	}
}

func TestIndexAllBackfillsEveryChatSession(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	first := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	second := seedChatSession(t, f.store, "proj", "sess-2", "conv-2")
	appendUserMessage(t, f.store, first, "conv-1", "msg-1", "turn-1", "alpha needle")
	appendUserMessage(t, f.store, second, "conv-2", "msg-2", "turn-2", "beta needle")

	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	results := search(t, f.svc, "needle")
	if len(results.Results) != 2 {
		t.Fatalf("results = %+v, want both sessions", results.Results)
	}
	coverage, err := f.svc.Coverage(context.Background())
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if coverage.BackfilledAt.IsZero() {
		t.Fatal("a completed backfill records its finish time")
	}
}

func TestSearchAppliesProjectHarnessAndDateFilters(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	first := seedChatSession(t, f.store, "alpha", "sess-1", "conv-1")
	appendUserMessage(t, f.store, first, "conv-1", "msg-1", "turn-1", "shared needle")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(first.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}

	byProject := search(t, f.svc, "needle project:alpha")
	if len(byProject.Results) != 1 {
		t.Fatalf("project filter = %+v", byProject.Results)
	}
	byOtherProject := search(t, f.svc, "needle project:beta")
	if len(byOtherProject.Results) != 0 {
		t.Fatalf("other project filter = %+v", byOtherProject.Results)
	}
	byHarness := search(t, f.svc, "needle harness:codex")
	if len(byHarness.Results) != 0 {
		t.Fatalf("harness filter = %+v", byHarness.Results)
	}
	byMachine := search(t, f.svc, "needle machine:test-machine")
	if len(byMachine.Results) != 1 {
		t.Fatalf("machine filter = %+v", byMachine.Results)
	}
	byOtherMachine := search(t, f.svc, "needle machine:other-machine")
	if len(byOtherMachine.Results) != 0 {
		t.Fatalf("other machine filter = %+v", byOtherMachine.Results)
	}
	byDate := search(t, f.svc, "needle after:2027-01-01")
	if len(byDate.Results) != 0 {
		t.Fatalf("after: filter = %+v", byDate.Results)
	}
	byBefore := search(t, f.svc, "needle before:2027-01-01")
	if len(byBefore.Results) != 1 {
		t.Fatalf("before: filter = %+v", byBefore.Results)
	}
}

func TestSearchFiltersBySessionStatus(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	live := seedChatSession(t, f.store, "proj", "sess-live", "conv-live")
	appendUserMessage(t, f.store, live, "conv-live", "msg-live", "turn-live", "status probe live")
	dead := seedChatSession(t, f.store, "proj", "sess-dead", "conv-dead")
	appendUserMessage(t, f.store, dead, "conv-dead", "msg-dead", "turn-dead", "status probe killed")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	// Kill the second session through the durable fact the status filter reads.
	record, found, err := f.store.GetSession(context.Background(), dead.ID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.IsTerminated = true
	if err := f.store.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	killed := search(t, f.svc, "status probe status:killed")
	if len(killed.Results) != 1 || killed.Results[0].SessionID != string(dead.ID) {
		t.Fatalf("status:killed = %+v, want only the terminated session", killed.Results)
	}
	liveResults := search(t, f.svc, "status probe status:live")
	if len(liveResults.Results) != 1 || liveResults.Results[0].SessionID != string(live.ID) {
		t.Fatalf("status:live = %+v, want only the live session", liveResults.Results)
	}
}

// Requirement: full-text search must be case-insensitive, prefix-matching, and
// phrase-aware. Each is a separate FTS5 behavior, so each gets its own
// assertion against one seeded document rather than one combined query.
func TestSearchCasePrefixAndPhrase(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-case", "conv-case")
	appendUserMessage(t, f.store, session, "conv-case", "msg-1", "turn-1", "The Flaky Login Test needs an await")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"lowercase", "flaky", 1},
		{"uppercase query", "FLAKY", 1},
		{"mixed case query", "FlAkY", 1},
		{"uppercase stored word", "LOGIN", 1},
		{"prefix of a word", "fla", 1},
		{"prefix of a later word", "awa", 1},
		{"quoted phrase in order", `"flaky login"`, 1},
		{"quoted phrase out of order", `"login flaky"`, 0},
		{"phrase spanning three words", `"needs an await"`, 1},
		{"phrase that is not contiguous", `"flaky needs"`, 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := search(t, f.svc, testCase.query)
			if len(got.Results) != testCase.want {
				t.Fatalf("%q matched %d results, want %d", testCase.query, len(got.Results), testCase.want)
			}
		})
	}
}

// Tokenization must make code identifiers findable by their parts, through the
// real query path rather than only in the splitter's unit test.
func TestSearchFindsIdentifierParts(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-ident", "conv-ident")
	appendUserMessage(t, f.store, session, "conv-ident", "msg-1", "turn-1",
		"refactor parseHTTPResponse and the snake_case_name helper in src/auth/login_test.ts")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	for _, term := range []string{"parseHTTPResponse", "http", "response", "snake", "case", "login", "auth", "ts"} {
		t.Run(term, func(t *testing.T) {
			if got := search(t, f.svc, term); len(got.Results) == 0 {
				t.Fatalf("%q is not findable", term)
			}
		})
	}
}

// Requirement: a hibernated session is as searchable as a live one, and its
// result says it is hibernated so the click can explain what it will do.
func TestHibernatedSessionsAreSearchableAndLabelled(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-hibernate", "conv-hibernate")
	appendUserMessage(t, f.store, session, "conv-hibernate", "msg-1", "turn-1", "hibernation needle content")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	record, found, err := f.store.GetSession(context.Background(), session.ID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	at := testNow
	if applied, err := f.store.SetSessionHibernated(context.Background(), session.ID, record.Revision, &at); err != nil || !applied {
		t.Fatalf("SetSessionHibernated: applied=%v err=%v", applied, err)
	}

	got := search(t, f.svc, "hibernation needle")
	if len(got.Results) != 1 {
		t.Fatalf("a hibernated session is not searchable: %+v", got.Results)
	}
	if got.Results[0].SessionState != "hibernated" {
		t.Fatalf("session state = %q, want hibernated so the UI can explain the click", got.Results[0].SessionState)
	}
	// status:hibernated selects it and status:live does not.
	if hibernated := search(t, f.svc, "hibernation needle status:hibernated"); len(hibernated.Results) != 1 {
		t.Fatalf("status:hibernated = %+v", hibernated.Results)
	}
	if live := search(t, f.svc, "hibernation needle status:live"); len(live.Results) != 0 {
		t.Fatalf("a hibernated session matched status:live: %+v", live.Results)
	}
}

// The total must be how many results matched, not how many fit on the page.
// Orca shows "12 shown · 47 recent" and offers "Show more"; neither is possible
// if the daemon reports a page length as the count. Per message, that is every
// matching document.
func TestSearchTotalCountsMatchesNotPageSize(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-total", "conv-total")
	for index := 0; index < 8; index++ {
		appendUserMessage(t, f.store, session, "conv-total",
			"msg-"+string(rune('a'+index)), "turn-"+string(rune('a'+index)),
			"the shared needle appears here number "+string(rune('a'+index)))
	}
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	parsed := domain.ParseSessionSearchQuery("needle")
	page, err := f.svc.Search(context.Background(), parsed, domain.SessionSearchParams{Limit: 3, Group: domain.SessionSearchGroupMessage})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 3 {
		t.Fatalf("page returned %d results, want the 3 asked for", len(page.Results))
	}
	if page.Total < 8 {
		t.Fatalf("total = %d, want all matching documents rather than the page size", page.Total)
	}
	// The second page must continue rather than repeat the first.
	second, err := f.svc.Search(context.Background(), parsed, domain.SessionSearchParams{Limit: 3, Offset: 3, Group: domain.SessionSearchGroupMessage})
	if err != nil {
		t.Fatalf("Search page 2: %v", err)
	}
	if len(second.Results) == 0 {
		t.Fatal("second page is empty")
	}
	first := map[string]struct{}{}
	for _, result := range page.Results {
		first[result.SessionID+":"+result.MessageID] = struct{}{}
	}
	for _, result := range second.Results {
		if _, repeated := first[result.SessionID+":"+result.MessageID]; repeated {
			t.Fatalf("page 2 repeated %s/%s from page 1", result.SessionID, result.MessageID)
		}
	}
}

// Grouped by session (the default), a session is one row at its best match, its
// row counts the rest, and the total counts sessions. Without this, one long
// session fills the palette's eight rows and hides every other session.
func TestSearchGroupsResultsBySession(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	busy := seedChatSession(t, f.store, "proj", "sess-busy", "conv-busy")
	for index := 0; index < 6; index++ {
		appendUserMessage(t, f.store, busy, "conv-busy",
			"busy-"+string(rune('a'+index)), "busy-turn-"+string(rune('a'+index)),
			"the grouping needle appears in a long session, message "+string(rune('a'+index)))
	}
	quiet := seedChatSession(t, f.store, "proj", "sess-quiet", "conv-quiet")
	appendUserMessage(t, f.store, quiet, "conv-quiet", "quiet-a", "quiet-turn-a", "grouping needle")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	response := search(t, f.svc, "grouping needle")
	if response.Group != domain.SessionSearchGroupSession {
		t.Fatalf("group = %q, want session by default", response.Group)
	}
	if len(response.Results) != 2 || response.Total != 2 {
		t.Fatalf("results = %s (total %d), want each session once", describe(response.Results), response.Total)
	}
	matches := map[string]int{}
	for _, result := range response.Results {
		matches[result.SessionID] = result.SessionMatches
	}
	if matches[string(busy.ID)] != 6 || matches[string(quiet.ID)] != 1 {
		t.Fatalf("session matches = %v, want 6 for the long session and 1 for the short one", matches)
	}
	// The row a session is shown at is its best match, so it opens there.
	for _, result := range response.Results {
		if result.MessageID == "" {
			t.Fatalf("grouped row %s has no message to open at", result.SessionID)
		}
	}
	perMessage, err := f.svc.Search(context.Background(), domain.ParseSessionSearchQuery("grouping needle"),
		domain.SessionSearchParams{Group: domain.SessionSearchGroupMessage})
	if err != nil {
		t.Fatalf("Search per message: %v", err)
	}
	if len(perMessage.Results) != 7 || perMessage.Total != 7 {
		t.Fatalf("per-message results = %d (total %d), want all 7 documents", len(perMessage.Results), perMessage.Total)
	}
}

func TestSearchRejectsInvertedDateRange(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	parsed := domain.ParseSessionSearchQuery("needle after:2026-08-01 before:2026-01-01")
	if _, err := f.svc.Search(context.Background(), parsed, domain.SessionSearchParams{}); err == nil {
		t.Fatal("inverted date range accepted")
	}
}

func TestSearchHighlightsAreOffsetsNotMarkup(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "the flaky login test")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	results := search(t, f.svc, "flaky")
	if len(results.Results) != 1 {
		t.Fatalf("results = %+v", results.Results)
	}
	hit := results.Results[0]
	if len(hit.Highlights) != 1 {
		t.Fatalf("highlights = %+v, want one span", hit.Highlights)
	}
	span := hit.Highlights[0]
	if span.Start < 0 || span.End > len(hit.Snippet) || span.Start >= span.End {
		t.Fatalf("highlight %+v outside snippet %q", span, hit.Snippet)
	}
	if matched := hit.Snippet[span.Start:span.End]; matched != "flaky" {
		t.Fatalf("highlighted text = %q, want flaky", matched)
	}
}

func TestSplitSnippetConvertsSentinels(t *testing.T) {
	text, highlights := sessionsearch.SplitSnippet("before \x02needle\x03 after")
	if text != "before needle after" {
		t.Fatalf("text = %q", text)
	}
	if len(highlights) != 1 || text[highlights[0].Start:highlights[0].End] != "needle" {
		t.Fatalf("highlights = %+v", highlights)
	}
	plain, none := sessionsearch.SplitSnippet("no markers here")
	if plain != "no markers here" || none != nil {
		t.Fatalf("plain = %q, highlights = %+v", plain, none)
	}
}

func TestCoverageReportsUnsupportedHarnesses(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	if err := f.store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "proj", Path: "/tmp/proj", RegisteredAt: testNow,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ID:        "sess-tui",
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessAider,
		Mode:      domain.SessionModeTUI,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: testNow},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	coverage, err := f.svc.Coverage(ctx)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if coverage.UnsupportedSources != 1 {
		t.Fatalf("unsupported sources = %d, want 1", coverage.UnsupportedSources)
	}
	if len(coverage.UnindexedHarnesses) != 1 || coverage.UnindexedHarnesses[0] != "aider" {
		t.Fatalf("unindexed harnesses = %v, want [aider]", coverage.UnindexedHarnesses)
	}
}

func TestBrowseOrdersByRecencyWithoutAQuery(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	older := seedChatSession(t, f.store, "proj", "sess-old", "conv-old")
	appendUserMessage(t, f.store, older, "conv-old", "msg-old", "turn-old", "older session")
	newer := seedChatSession(t, f.store, "proj", "sess-new", "conv-new")
	appendUserMessage(t, f.store, newer, "conv-new", "msg-new", "turn-new", "newer session")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	response := search(t, f.svc, "project:proj")
	if response.Sort != domain.SessionSearchSortRecent {
		t.Fatalf("sort = %q, want recent for a filter-only browse", response.Sort)
	}
	if len(response.Results) < 2 {
		t.Fatalf("browse returned %d rows, want the project's sessions", len(response.Results))
	}
}

// A standalone session belongs to no project: its project_id is NULL. One such
// row used to fail the backfill's first query outright, so a machine with any
// standalone session indexed nothing at all and reported "backfilling" forever.
func TestStandaloneSessionsAreIndexedSearchedAndBrowsed(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	project := seedChatSession(t, f.store, "proj", "sess-project", "conv-project")
	appendUserMessage(t, f.store, project, "conv-project", "msg-project", "turn-project", "project kettle notes")
	standalone, err := f.store.CreateSession(ctx, domain.SessionRecord{
		Kind:     domain.KindWorker,
		Harness:  domain.HarnessClaudeCode,
		Mode:     domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: testNow},
		Metadata: domain.SessionMetadata{
			ProviderConversationID: "thread-standalone",
			ControllerGeneration:   "generation-standalone",
		},
		CreatedAt: testNow.Add(time.Minute),
		UpdatedAt: testNow.Add(time.Minute),
	})
	if err != nil || !standalone.IsStandalone() {
		t.Fatalf("CreateSession standalone: %+v err=%v", standalone, err)
	}
	if _, err := f.store.CreateConversation(ctx, "conv-standalone", domain.ConversationScopeSession,
		"", standalone.ID, testNow); err != nil {
		t.Fatalf("CreateConversation standalone: %v", err)
	}
	created, err := f.store.AppendUserMessage(ctx, "conv-standalone", standalone.ID, "generation-standalone",
		domain.ConversationMessage{
			ID: "msg-standalone", Origin: domain.MessageOriginHuman, Text: "descale the kettle",
			ClientMessageID: "client-msg-standalone",
		}, "turn-standalone", testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage standalone: created=%v err=%v", created, err)
	}

	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll with a standalone session: %v", err)
	}
	coverage, err := f.svc.Coverage(ctx)
	if err != nil || coverage.State != "ready" || coverage.IndexedSources == 0 {
		t.Fatalf("coverage = %+v err=%v, want a ready index", coverage, err)
	}

	found := search(t, f.svc, "descale")
	if len(found.Results) != 1 || found.Results[0].SessionID != string(standalone.ID) || found.Results[0].ProjectID != "" {
		t.Fatalf("search descale = %+v, want the standalone session with no project", found.Results)
	}
	both := search(t, f.svc, "kettle")
	if len(both.Results) != 2 {
		t.Fatalf("search kettle = %d rows, want both sessions", len(both.Results))
	}

	// The recents list Cmd+K shows before anything is typed.
	recents := search(t, f.svc, "sort:recent")
	listed := map[string]bool{}
	for _, hit := range recents.Results {
		listed[hit.SessionID] = true
	}
	if !listed[string(standalone.ID)] || !listed[string(project.ID)] {
		t.Fatalf("recents = %+v, want the standalone and the project session", recents.Results)
	}
}

// failingTargetsStore fails the backfill's session listing while fail is set.
type failingTargetsStore struct {
	*sqlite.Store
	fail bool
}

func (s *failingTargetsStore) ListSessionSearchTargets(ctx context.Context) ([]domain.SessionSearchTarget, error) {
	if s.fail {
		return nil, fmt.Errorf("list session search targets: unreadable row")
	}
	return s.Store.ListSessionSearchTargets(ctx)
}

// A failed backfill must not read as "still indexing": nothing is filling the
// index. It reports unavailable, and a later backfill (the daemon retries on its
// sweep timer) brings it to ready.
func TestFailedBackfillReportsUnavailableUntilARetrySucceeds(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	flaky := &failingTargetsStore{Store: store, fail: true}
	svc := sessionsearch.New(sessionsearch.Options{
		Store: flaky, Machine: "test-machine", Namer: store,
		Now: func() time.Time { return testNow },
	})
	ctx := context.Background()
	session := seedChatSession(t, store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, store, session, "conv-1", "msg-1", "turn-1", "rotate the signing key")

	if _, err := svc.IndexAll(ctx); err == nil {
		t.Fatal("IndexAll succeeded with an unreadable session listing")
	}
	coverage, err := svc.Coverage(ctx)
	if err != nil || coverage.State != "unavailable" || svc.Backfilled() {
		t.Fatalf("after a failed backfill: coverage=%+v backfilled=%v err=%v, want unavailable", coverage, svc.Backfilled(), err)
	}

	flaky.fail = false
	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("retry IndexAll: %v", err)
	}
	coverage, err = svc.Coverage(ctx)
	if err != nil || coverage.State != "ready" || !svc.Backfilled() {
		t.Fatalf("after the retry: coverage=%+v backfilled=%v err=%v, want ready", coverage, svc.Backfilled(), err)
	}
	if found := search(t, svc, "signing"); len(found.Results) != 1 {
		t.Fatalf("search after the retry = %d rows, want 1", len(found.Results))
	}
}

func TestMatchesForSessionListsEveryMatchInOrder(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	session := seedChatSession(t, f.store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, f.store, session, "conv-1", "msg-1", "turn-1", "first retry mention")
	settleAssistantMessage(t, f.store, "conv-1", "msg-1-assistant", "turn-1", "second retry mention")
	appendUserMessage(t, f.store, session, "conv-1", "msg-2", "turn-2", "third retry mention")
	if _, err := f.svc.IndexChat(context.Background(), conversationFor(t, f.store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}
	parsed := domain.ParseSessionSearchQuery("retry")
	matches, err := f.svc.MatchesForSession(context.Background(), string(session.ID), parsed.MatchQuery)
	if err != nil {
		t.Fatalf("MatchesForSession: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("matches = %d, want 3", len(matches))
	}
	for index := 1; index < len(matches); index++ {
		if matches[index].Seq < matches[index-1].Seq {
			t.Fatalf("matches are not ordered by position: %+v", matches)
		}
	}
}

// The ranking stage scores at most 2,000 candidates by raw bm25. Two promises
// keep that invisible: the total stays exact (it is recounted when the cap is
// reached), and a filter turns the cap off, because a filter could otherwise
// reject every capped candidate and hide a match that exists.
func TestSearchCandidateCapKeepsTotalsAndFiltersExact(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	const broad = 2100
	docs := make([]domain.SessionSearchDoc, 0, broad+1)
	for index := 0; index < broad; index++ {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: "chat:busy", DocKey: fmt.Sprintf("m%d", index), SessionID: "busy", ProjectID: "main",
			Kind: domain.SessionSearchKindUser, MessageID: fmt.Sprintf("m%d", index), Seq: int64(index),
			Body: "capneedle capneedle capneedle", CreatedAt: testNow, IndexedAt: testNow,
		})
	}
	// The weakest match in the index (one mention in a long body), in its own
	// project: outside the top 2,000 by raw bm25.
	docs = append(docs, domain.SessionSearchDoc{
		SourceKey: "chat:quiet", DocKey: "q1", SessionID: "quiet", ProjectID: "other",
		Kind: domain.SessionSearchKindUser, MessageID: "q1", Seq: 1,
		Body: "capneedle " + strings.Repeat("unrelated filler words ", 200), CreatedAt: testNow, IndexedAt: testNow,
	})
	if err := f.store.UpsertSessionSearchDocs(ctx, docs); err != nil {
		t.Fatalf("UpsertSessionSearchDocs: %v", err)
	}
	perMessage, err := f.svc.Search(ctx, domain.ParseSessionSearchQuery("capneedle"),
		domain.SessionSearchParams{Group: domain.SessionSearchGroupMessage, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if perMessage.Total != broad+1 {
		t.Fatalf("total = %d, want the exact %d even though ranking stopped at the cap", perMessage.Total, broad+1)
	}
	filtered := search(t, f.svc, "capneedle project:other")
	if len(filtered.Results) != 1 || filtered.Results[0].SessionID != "quiet" {
		t.Fatalf("project filter lost a match ranked outside the cap: %s", describe(filtered.Results))
	}
}

// Highlight offsets are UTF-16 code units, the unit the renderer slices
// JavaScript strings by. With byte offsets, every match after a non-ASCII
// character was highlighted on the wrong letters.
func TestSplitSnippetOffsetsAreUTF16(t *testing.T) {
	marked := "café ✓ 😀 日本 " + domain.SessionSearchSnippetStart + "needle" + domain.SessionSearchSnippetEnd + " done"
	text, highlights := sessionsearch.SplitSnippet(marked)
	if len(highlights) != 1 {
		t.Fatalf("highlights = %+v", highlights)
	}
	units := utf16.Encode([]rune(text))
	got := string(utf16.Decode(units[highlights[0].Start:highlights[0].End]))
	if got != "needle" {
		t.Fatalf("highlight covers %q, want %q (span %+v in %q)", got, "needle", highlights[0], text)
	}
}

// A reviewer chat is part of the session it reviews, so its words must be as
// findable as the session's own. It opens in the reviewer chat while the app can
// show that transcript (the review is live in Chat); after the review is
// switched to the terminal or archived (ending a session archives its reviews),
// the words stay searchable and the result opens the session instead of a
// reviewer chat that cannot render.
func TestSearchFindsReviewerChatsAndOpensThemWhileOpenable(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-review", "conv-worker")
	appendUserMessage(t, f.store, session, "conv-worker", "worker-msg", "worker-turn", "the worker side of the platypus work")
	review := domain.Review{
		ID: "review-1", SessionID: session.ID, ProjectID: session.ProjectID,
		Harness: domain.ReviewerCodex, CreatedAt: testNow, UpdatedAt: testNow,
	}
	if err := f.store.UpsertReview(ctx, review); err != nil {
		t.Fatalf("UpsertReview: %v", err)
	}
	conversation, err := f.store.CreateReviewConversation(ctx, "conv-review", review.ID, session.ProjectID, session.ID, testNow)
	if err != nil {
		t.Fatalf("CreateReviewConversation: %v", err)
	}
	if claimed, err := f.store.ClaimReviewChatController(ctx, review.ID, "review-provider", "review-generation", testNow); err != nil || !claimed {
		t.Fatalf("ClaimReviewChatController: claimed=%v err=%v", claimed, err)
	}
	if created, err := f.store.AppendReviewUserMessage(ctx, conversation.ID, session.ID, review.ID, "review-generation", domain.ConversationMessage{
		ID: "review-msg", Text: "the reviewer flagged a quokka regression", Origin: domain.MessageOriginHuman,
	}, "review-turn", testNow); err != nil || !created {
		t.Fatalf("AppendReviewUserMessage: created=%v err=%v", created, err)
	}
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	got := search(t, f.svc, "quokka")
	if len(got.Results) != 1 || got.Results[0].SessionID != string(session.ID) ||
		got.Results[0].ReviewID != review.ID || got.Results[0].MessageID != "review-msg" {
		t.Fatalf("reviewer chat hit = %+v, want the session, the review and the message", got.Results)
	}
	worker := search(t, f.svc, "platypus")
	if len(worker.Results) != 1 || worker.Results[0].ReviewID != "" || worker.Results[0].MessageID != "worker-msg" {
		t.Fatalf("worker hit = %+v, want the session's own message with no review", worker.Results)
	}
	// Stepping walks the session's own transcript, which does not hold the
	// reviewer's messages.
	if matches, err := f.svc.MatchesForSession(ctx, string(session.ID), domain.ParseSessionSearchQuery("quokka").MatchQuery); err != nil || len(matches) != 0 {
		t.Fatalf("in-session matches = %+v err=%v, want none from the reviewer chat", matches, err)
	}
	// Two sources for one session must not rewrite each other on every pass.
	if written, err := f.svc.Sweep(ctx); err != nil || written != 0 {
		t.Fatalf("sweep after backfill wrote %d (err %v), want 0", written, err)
	}

	if changed, err := f.store.SetReviewInterfaceMode(ctx, review.ID, domain.ReviewerInterfaceTUI, testNow.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("SetReviewInterfaceMode: changed=%v err=%v", changed, err)
	}
	terminal := search(t, f.svc, "quokka")
	if len(terminal.Results) != 1 || terminal.Results[0].ReviewID != "" || terminal.Results[0].MessageID != "" {
		t.Fatalf("terminal reviewer hit = %+v, want it found but opening the session", terminal.Results)
	}
	if err := f.store.ArchiveReviewsBySession(ctx, session.ID, testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("ArchiveReviewsBySession: %v", err)
	}
	if archived := search(t, f.svc, "quokka"); len(archived.Results) != 1 || archived.Results[0].ReviewID != "" {
		t.Fatalf("archived reviewer hit = %+v, want it still found", archived.Results)
	}
}

func renameForSearch(t *testing.T, f fixture, session domain.SessionRecord, name string) {
	t.Helper()
	if _, err := f.store.RenameSession(context.Background(), session.ID, name, time.Now()); err != nil {
		t.Fatalf("RenameSession %s: %v", session.ID, err)
	}
}

// One row per session, ranked by one blended score in which the name weighs
// most, but shown at its best-matching MESSAGE: the snippet is what was said,
// Enter opens there, and the count is how many messages matched (a message and
// the command shown with it count once; the name is not a message).
func TestGroupedRowShowsTheBestMessageAndCountsMessages(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	named := seedChatSession(t, f.store, "proj", "sess-named", "conv-named")
	renameForSearch(t, f, named, "Flaky login investigation")
	appendUserMessage(t, f.store, named, "conv-named", "named-user", "named-turn", "the login test flakes on CI")
	settleAssistantMessage(t, f.store, "conv-named", "named-reply", "named-turn", "Fixed the flaky login race in the helper.")
	addCommandActivity(t, f.store, "conv-named", "named-turn", "named-cmd", "go test ./login",
		"--- FAIL: flaky login spec", domain.ActivityStatusFailed)
	mention := seedChatSession(t, f.store, "proj", "sess-mention", "conv-mention")
	appendUserMessage(t, f.store, mention, "conv-mention", "mention-user", "mention-turn", "saw a flaky login once")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	got := search(t, f.svc, "flaky login")
	if len(got.Results) != 2 || got.Results[0].SessionID != string(named.ID) || got.Results[1].SessionID != string(mention.ID) {
		t.Fatalf("results = %s, want the named session first, each session once", describe(got.Results))
	}
	top := got.Results[0]
	// Both messages match both words; the person's own words weigh more.
	if top.Kind == domain.SessionSearchKindTitle || top.MessageID != "named-user" {
		t.Fatalf("top row = %s %q, want the best-matching message, not the name", top.Kind, top.MessageID)
	}
	if top.Snippet != "the login test flakes on CI" || len(top.Highlights) != 2 {
		t.Fatalf("snippet = %q highlights %v, want the message with login and flakes marked", top.Snippet, top.Highlights)
	}
	// named-user and the reply; the failing command is shown with named-user.
	if top.SessionMatches != 2 || got.Results[1].SessionMatches != 1 {
		t.Fatalf("match counts = %d and %d, want 2 and 1", top.SessionMatches, got.Results[1].SessionMatches)
	}
	// The find bar steps through exactly the messages the row counted.
	matches, err := f.svc.MatchesForSession(context.Background(), string(named.ID), domain.ParseSessionSearchQuery("flaky login").MatchQuery)
	if err != nil {
		t.Fatalf("MatchesForSession: %v", err)
	}
	stops := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.MessageID != "" {
			stops = append(stops, match.MessageID)
		}
	}
	// settleAssistantMessage stores the reply as "assistant-" + its id.
	if strings.Join(stops, ",") != "named-user,assistant-named-reply" {
		t.Fatalf("find bar stops = %v, want each matched message once, in order", stops)
	}
}

// Session metadata is part of the blended score at a lower weight than the
// name: a session found only by its branch ranks below one whose name matches,
// and its row (no message matched) opens the session at the top.
func TestBranchMetadataRanksBelowTheName(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	// seedChatSession creates every session on branch "feat/search".
	byBranch := seedChatSession(t, f.store, "proj", "sess-branch", "conv-branch")
	renameForSearch(t, f, byBranch, "Tidy the sidebar")
	appendUserMessage(t, f.store, byBranch, "conv-branch", "branch-user", "branch-turn", "move the buttons left")
	byName := seedChatSession(t, f.store, "proj", "sess-name", "conv-name")
	renameForSearch(t, f, byName, "Search ranking tune-up")
	if _, err := f.svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	got := search(t, f.svc, "search")
	if len(got.Results) != 2 || got.Results[0].SessionID != string(byName.ID) || got.Results[1].SessionID != string(byBranch.ID) {
		t.Fatalf("results = %s, want the name match above the branch match", describe(got.Results))
	}
	if branchRow := got.Results[1]; branchRow.MessageID != "" || branchRow.SessionMatches != 0 {
		t.Fatalf("branch-only row = %q with %d matches, want it to open the session with no message count", branchRow.MessageID, branchRow.SessionMatches)
	}
}
