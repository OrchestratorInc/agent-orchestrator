package sessionsearch_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestIndexFootprintStaysSmallRelativeToSourceText(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})

	var output strings.Builder
	for line := 0; line < 2000; line++ {
		output.WriteString("line " + strconv.Itoa(line) + " of command output\n")
	}
	for conversation := 0; conversation < 12; conversation++ {
		id := "conv-" + strconv.Itoa(conversation)
		session := seedChatSession(t, store, "proj", "sess-"+strconv.Itoa(conversation), id)
		for message := 0; message < 20; message++ {
			appendUserMessage(t, store, session, id,
				"m"+strconv.Itoa(conversation)+"-"+strconv.Itoa(message),
				"t"+strconv.Itoa(conversation)+"-"+strconv.Itoa(message),
				"flaky login test number "+strconv.Itoa(message))
		}
		for activity := 0; activity < 20; activity++ {
			addCommandActivity(t, store, id, "t"+strconv.Itoa(conversation)+"-0",
				"cmd-"+strconv.Itoa(conversation)+"-"+strconv.Itoa(activity),
				"go test ./...", output.String(), domain.ActivityStatusFailed)
		}
	}
	if _, err := svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	db := openRawWithIndex(t, dir)
	var indexText, sourceText int64
	if err := db.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(body) + LENGTH(paths) + LENGTH(title)), 0) FROM session_search_text`,
	).Scan(&indexText); err != nil {
		t.Fatalf("measure index text: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(command_output)), 0) FROM conversation_activities`,
	).Scan(&sourceText); err != nil {
		t.Fatalf("measure source text: %v", err)
	}
	if sourceText == 0 {
		t.Fatal("fixture stored no source text; the ratio would be meaningless")
	}
	ratio := float64(indexText) / float64(sourceText)
	t.Logf("index text %d KB vs source text %d KB (%.1f%%)", indexText/1024, sourceText/1024, ratio*100)
	if ratio > 0.35 {
		t.Fatalf("index stores %.0f%% of the source text; it must stay a bounded excerpt", ratio*100)
	}
}

func TestOneHugeCommandIsBounded(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})
	session := seedChatSession(t, store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, store, session, "conv-1", "m1", "t1", "run the build")
	huge := "linker error: undefined symbol zzNeedle\n" + strings.Repeat("x", 60<<10) + "\nzzTailNeedle"
	addCommandActivity(t, store, "conv-1", "t1", "cmd-1", "make build", huge, domain.ActivityStatusFailed)
	if _, err := svc.IndexChat(context.Background(), conversationFor(t, store, string(session.ID))); err != nil {
		t.Fatalf("IndexChat: %v", err)
	}

	db := openRawWithIndex(t, dir)
	var maxBody int64
	if err := db.QueryRow(
		`SELECT COALESCE(MAX(LENGTH(body)), 0) FROM session_search_text`,
	).Scan(&maxBody); err != nil {
		t.Fatalf("measure body length: %v", err)
	}
	if maxBody > 16<<10 {
		t.Fatalf("one document is %d bytes; the per-document budget must bound it", maxBody)
	}
	if got := search(t, svc, "zzNeedle"); len(got.Results) != 1 {
		t.Fatalf("the start of a huge command was lost: %+v", got.Results)
	}
	if got := search(t, svc, "zzTailNeedle"); len(got.Results) != 0 {
		t.Fatalf("output past its first 3,072 characters is not indexed: %+v", got.Results)
	}
}

func TestIndexingStoresDocumentsAndTracksTheSource(t *testing.T) {
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := sessionsearch.New(sessionsearch.Options{Store: store, Now: func() time.Time { return testNow }})
	session := seedChatSession(t, store, "proj", "sess-1", "conv-1")
	appendUserMessage(t, store, session, "conv-1", "m1", "t1", "a searchable phrase")

	if count, err := store.CountSessionSearchDocs(context.Background()); err != nil || count != 0 {
		t.Fatalf("index starts non-empty: count=%d err=%v", count, err)
	}
	if _, err := svc.IndexAll(context.Background()); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	count, err := store.CountSessionSearchDocs(context.Background())
	if err != nil {
		t.Fatalf("count documents: %v", err)
	}
	if count == 0 {
		t.Fatal("indexing stored no documents")
	}
	source, ok, err := store.GetSessionSearchSource(context.Background(), sessionsearch.ChatSourceKey(string(session.ID)))
	if err != nil || !ok {
		t.Fatalf("source row missing: ok=%v err=%v", ok, err)
	}
	if source.State != domain.SessionSearchSourceActive || source.DocCount == 0 {
		t.Fatalf("source row = %+v, want an active source with documents", source)
	}
	if source.Stamp == "" {
		t.Fatal("no stamp recorded; the sweep would re-read every conversation forever")
	}
}

func openRawWithIndex(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`ATTACH DATABASE ? AS searchidx`, filepath.Join(dir, "search.db")); err != nil {
		t.Fatalf("attach search database: %v", err)
	}
	return db
}
