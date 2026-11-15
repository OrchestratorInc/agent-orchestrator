package sessionsearch_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// A restart must not re-index the world. The cursor rows are durable, so a new
// service over the same database should skip everything it already covered and
// still pick up work that arrived while it was down.
func TestRestartResumesWithoutReindexing(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	first := sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})
	session := seedChatSession(t, store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, store, session, "conv-1", "msg-1", "turn-1", "the first searchable message")
	if _, err := first.IndexAll(ctx); err != nil {
		t.Fatalf("first IndexAll: %v", err)
	}
	if got := search(t, first, "searchable"); len(got.Results) != 1 {
		t.Fatalf("first pass: %+v", got.Results)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// "Restart": a new store handle and a new service over the same data dir.
	reopened, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	second := sessionsearch.New(sessionsearch.Options{Store: reopened, Now: func() time.Time { return testNow }})

	// Everything already indexed is still searchable before any new pass.
	if got := search(t, second, "searchable"); len(got.Results) != 1 {
		t.Fatalf("index did not survive the restart: %+v", got.Results)
	}
	// A backfill after restart must write nothing: the cursors say it is done.
	written, err := second.IndexAll(ctx)
	if err != nil {
		t.Fatalf("second IndexAll: %v", err)
	}
	if written != 0 {
		t.Fatalf("restart re-indexed %d documents, want 0", written)
	}
	// Work that arrived while the service was down is picked up.
	record, found, err := reopened.GetSession(ctx, session.ID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	appendUserMessage(t, reopened, record, "conv-1", "msg-2", "turn-2", "a message about zebras")
	if _, err := second.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after restart: %v", err)
	}
	if got := search(t, second, "zebras"); len(got.Results) != 1 {
		t.Fatalf("message added while stopped was not indexed: %+v", got.Results)
	}
}

// A native transcript must also resume from its stored byte offset, not from
// zero, or every restart would duplicate its documents.
func TestRestartResumesNativeTranscriptsFromTheirOffset(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	ctx := context.Background()

	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	roots := sessionsearch.TranscriptRoots{ClaudeProjects: root}
	first := sessionsearch.New(sessionsearch.Options{Store: store, Roots: roots, Now: func() time.Time { return testNow }})
	seedTUIProject(t, store, "proj")
	session := seedTUI(t, store, "proj", "sess-native", "claude-code")
	writeTranscript(t, root, strings.ReplaceAll("/tmp/proj", "/", "-")+"/"+string(session.ID)+".jsonl",
		claudeLine(t, "user", "user", "the original prompt", "2026-08-09T12:00:00Z"),
	)
	if _, err := first.IndexAll(ctx); err != nil {
		t.Fatalf("first IndexAll: %v", err)
	}
	if got := search(t, first, "original"); len(got.Results) != 1 {
		t.Fatalf("first pass: %+v", got.Results)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	second := sessionsearch.New(sessionsearch.Options{Store: reopened, Roots: roots, Now: func() time.Time { return testNow }})
	written, err := second.IndexAll(ctx)
	if err != nil {
		t.Fatalf("second IndexAll: %v", err)
	}
	if written != 0 {
		t.Fatalf("restart re-indexed %d documents, want 0", written)
	}
	// Exactly one document for the line: a duplicated pass would leave two.
	if got := search(t, second, "original"); len(got.Results) != 1 {
		t.Fatalf("restart duplicated the transcript line: %+v", got.Results)
	}
}

// Found in review: a backup of ao.db carries the index's owner token, so
// restoring it beside the current search.db keeps the index, sessions created
// after the backup included. No delete ever recorded those, so they stayed in
// results (and opened nothing) for good. A backfill now checks the indexed
// sessions against the database.
func TestRestoringAnOlderDatabaseDropsTheSessionsItDoesNotHave(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	open := func() (*sqlite.Store, *sessionsearch.Service) {
		t.Helper()
		store, err := sqlite.Open(dir)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return store, sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})
	}
	closeStore := func(store *sqlite.Store) {
		t.Helper()
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}

	// Before the backup: two sessions, indexed.
	store, svc := open()
	kept := seedChatSession(t, store, "proj", "sess-kept", "conv-kept")
	appendUserMessage(t, store, kept, "conv-kept", "msg-1", "turn-1", "zzkept was written before the backup")
	doomed := seedChatSession(t, store, "proj", "sess-doomed", "conv-doomed")
	appendUserMessage(t, store, doomed, "conv-doomed", "msg-doomed", "turn-doomed", "zzdoomed is deleted after the backup")
	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll before the backup: %v", err)
	}
	closeStore(store)
	backup := map[string][]byte{}
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(filepath.Join(dir, "ao.db"+suffix))
		if err == nil {
			backup[suffix] = data
		} else if suffix == "" {
			t.Fatalf("back up the database: %v", err)
		}
	}

	// After it: another session, another message in the first, and the second
	// deleted, its documents removed by the sweep.
	store, svc = open()
	later := seedChatSession(t, store, "proj", "sess-later", "conv-later")
	appendUserMessage(t, store, later, "conv-later", "msg-later", "turn-later", "zzlater was created after the backup")
	appendUserMessage(t, store, kept, "conv-kept", "msg-2", "turn-2", "zzafterword was added after the backup")
	doomed.IsTerminated = true
	if err := store.UpdateSession(ctx, doomed); err != nil {
		t.Fatalf("archive the session to delete: %v", err)
	}
	if deleted, err := store.DeleteTerminatedSession(ctx, doomed.ID); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll after the backup: %v", err)
	}
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after the delete: %v", err)
	}
	if count, err := store.CountSessionSearchDocsForSource(ctx, "chat:"+string(doomed.ID)); err != nil || count != 0 {
		t.Fatalf("documents of the deleted session = %d (err %v), want 0 before the restore", count, err)
	}
	for _, word := range []string{"zzlater", "zzafterword"} {
		if got := search(t, svc, word).Results; len(got) != 1 {
			t.Fatalf("%s before the restore: %s", word, describe(got))
		}
	}
	closeStore(store)

	// Restore only ao.db; search.db stays as it is.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(filepath.Join(dir, "ao.db"+suffix))
	}
	for suffix, data := range backup {
		if err := os.WriteFile(filepath.Join(dir, "ao.db"+suffix), data, 0o600); err != nil {
			t.Fatalf("restore the database: %v", err)
		}
	}
	store, svc = open()
	t.Cleanup(func() { _ = store.Close() })
	if _, found, err := store.GetSession(ctx, later.ID); err != nil || found {
		t.Fatalf("the restored database must not have the later session (found=%v err=%v)", found, err)
	}
	// The index was kept, not rebuilt: it is the same database's, by its token.
	if count, err := store.CountSessionSearchDocsForSource(ctx, "chat:"+string(later.ID)); err != nil || count == 0 {
		t.Fatalf("documents of the later session after the restore = %d (err %v), want the kept index", count, err)
	}

	if _, err := svc.IndexAll(ctx); err != nil {
		t.Fatalf("IndexAll after the restore: %v", err)
	}
	// The session the database no longer has is not a result at once.
	if got := search(t, svc, "zzlater").Results; len(got) != 0 {
		t.Fatalf("results for a session the restored database does not have: %s", describe(got))
	}
	// A message the database no longer has goes with its conversation's re-read.
	if got := search(t, svc, "zzafterword").Results; len(got) != 0 {
		t.Fatalf("results for a message the restored database does not have: %s", describe(got))
	}
	if got := search(t, svc, "zzkept").Results; len(got) != 1 || got[0].SessionID != string(kept.ID) {
		t.Fatalf("the session the backup holds: %s", describe(got))
	}
	// A session deleted after the backup is back in the database; its documents
	// were removed with it, so it is indexed again.
	if got := search(t, svc, "zzdoomed").Results; len(got) != 1 || got[0].SessionID != string(doomed.ID) {
		t.Fatalf("the session the restore brought back: %s", describe(got))
	}
	// The sweep then removes the dropped session's documents and its record.
	if _, err := svc.Sweep(ctx); err != nil {
		t.Fatalf("Sweep after the restore: %v", err)
	}
	if count, err := store.CountSessionSearchDocsForSource(ctx, "chat:"+string(later.ID)); err != nil || count != 0 {
		t.Fatalf("documents of the dropped session after the sweep = %d (err %v), want 0", count, err)
	}
	if orphans, err := store.SessionSearchOrphanSessions(ctx, 10); err != nil || len(orphans) != 0 {
		t.Fatalf("sessions still recorded as deleted = %v (err %v), want none", orphans, err)
	}
	// Nothing is left to do: another backfill writes nothing and drops nothing.
	if written, err := svc.IndexAll(ctx); err != nil || written != 0 {
		t.Fatalf("a second backfill wrote %d (err %v), want 0", written, err)
	}
	if got := search(t, svc, "zzkept").Results; len(got) != 1 {
		t.Fatalf("the kept session after the cleanup: %s", describe(got))
	}
}
