package sessionsearch_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// longConversation seeds a session with many messages and a queued prompt at
// the end that the tests can edit.
func longConversation(t *testing.T, store *sqlite.Store, svc *sessionsearch.Service) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	session := seedChatSession(t, store, "proj", "sess-long", "conv-long")
	for i := range 30 {
		n := strconv.Itoa(i)
		appendUserMessage(t, store, session, "conv-long", "msg-"+n, "turn-"+n, "message "+n+" about the cache")
		settleAssistantMessage(t, store, "conv-long", "reply-"+n, "turn-"+n, "reply "+n+" about the cache")
	}
	created, err := store.AppendUserMessage(ctx, "conv-long", session.ID, "generation-"+string(session.ID),
		domain.ConversationMessage{ID: "queued-msg", Origin: domain.MessageOriginHuman, Text: "deploy zzbefore tonight", ClientMessageID: "client-queued"},
		"turn-queued", testNow)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	return session
}

// Found in the database review: every pass over a conversation rewrote all of
// its documents, so one edited message in a 7,500-document conversation was
// 7,500 index writes. A pass now writes only the documents whose content
// changed.
func TestAnEditWritesOnlyTheEditedDocument(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := longConversation(t, f.store, f.svc)
	before, err := f.store.CountSessionSearchDocsForSource(ctx, sessionsearch.ChatSourceKey(string(session.ID)))
	if err != nil || before < 60 {
		t.Fatalf("indexed %d documents (err %v), want the whole conversation", before, err)
	}
	if err := f.store.UpdateQueuedTurnMessage(ctx, "conv-long", "turn-queued", "deploy zzafter tonight", "", 0,
		testNow.Add(time.Minute), domain.ConversationQueuedEditDelivery{}); err != nil {
		t.Fatalf("UpdateQueuedTurnMessage: %v", err)
	}
	written, err := f.svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if written != 1 {
		t.Fatalf("an edit to one message wrote %d documents, want 1", written)
	}
	if got := search(t, f.svc, "zzbefore").Results; len(got) != 0 {
		t.Fatalf("the edited-away word is still searchable: %s", describe(got))
	}
	if len(search(t, f.svc, "zzafter").Results) != 1 {
		t.Fatal("the edited message was not re-indexed")
	}
	if after, _ := f.store.CountSessionSearchDocsForSource(ctx, sessionsearch.ChatSourceKey(string(session.ID))); after != before {
		t.Fatalf("documents = %d after the edit, want %d", after, before)
	}
}

// A rolled-back turn's documents are deleted, and nothing else is written.
func TestARollbackDeletesOnlyTheDiscardedDocuments(t *testing.T) {
	f := newFixture(t, sessionsearch.TranscriptRoots{})
	ctx := context.Background()
	session := seedChatSession(t, f.store, "proj", "sess-drop", "conv-drop")
	for i := range 10 {
		n := strconv.Itoa(i)
		appendUserMessage(t, f.store, session, "conv-drop", "keep-"+n, "turn-keep-"+n, "keep zzkeep "+n)
	}
	appendUserMessage(t, f.store, session, "conv-drop", "drop-msg", "turn-drop", "try zzdrop instead")
	addCommandActivity(t, f.store, "conv-drop", "turn-drop", "cmd-drop", "go test ./...", "FAIL zzdropoutput", domain.ActivityStatusCompleted)
	if _, err := f.svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	source := sessionsearch.ChatSourceKey(string(session.ID))
	before, err := f.store.CountSessionSearchDocsForSource(ctx, source)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if _, err := f.store.RollbackTurns(ctx, "conv-drop", "turn-drop", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("RollbackTurns: %v", err)
	}
	written, err := f.svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if written != 0 {
		t.Fatalf("a rollback wrote %d documents, want none (only deletes)", written)
	}
	after, err := f.store.CountSessionSearchDocsForSource(ctx, source)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if after >= before {
		t.Fatalf("documents = %d after the rollback, want fewer than %d", after, before)
	}
	for _, word := range []string{"zzdrop", "zzdropoutput"} {
		if got := search(t, f.svc, word).Results; len(got) != 0 {
			t.Fatalf("%s still searchable after the rollback: %s", word, describe(got))
		}
	}
	if len(search(t, f.svc, "zzkeep").Results) != 1 {
		t.Fatal("the kept turns are no longer searchable")
	}
}

// Found in the database review: when a conversation's stamp moved but nothing
// searchable changed (a revision or status bump), the pass did not record the
// new stamp, so every later sweep re-read the whole conversation (27 MB for
// the largest on a real archive).
func TestAChangeTheIndexCannotSeeRecordsTheNewStamp(t *testing.T) {
	dir := t.TempDir()
	store := sqlitetest.MustOpenAt(t, dir)
	svc := sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})
	ctx := context.Background()
	session := longConversation(t, store, svc)
	source := sessionsearch.ChatSourceKey(string(session.ID))
	before, ok, err := store.GetSessionSearchSource(ctx, source)
	if err != nil || !ok {
		t.Fatalf("GetSessionSearchSource: ok=%v err=%v", ok, err)
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`UPDATE conversation_messages SET revision = revision + 1, updated_at = ?
WHERE id = 'msg-3'`, testNow.Add(time.Hour)); err != nil {
		t.Fatalf("bump revision: %v", err)
	}

	written, err := svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if written != 0 {
		t.Fatalf("a revision bump wrote %d documents, want none", written)
	}
	after, _, err := store.GetSessionSearchSource(ctx, source)
	if err != nil {
		t.Fatalf("GetSessionSearchSource: %v", err)
	}
	if after.Stamp == before.Stamp {
		t.Fatalf("stamp unchanged (%q): the next sweep would read the conversation again", after.Stamp)
	}
	if after.Fingerprint != before.Fingerprint {
		t.Fatalf("fingerprint moved from %q to %q on a change the index cannot see", before.Fingerprint, after.Fingerprint)
	}
}
