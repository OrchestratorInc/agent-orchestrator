package sessionsearch_test

import (
	"context"
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
