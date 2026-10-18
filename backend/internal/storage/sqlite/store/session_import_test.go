package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestImportedSessionArchiveIsAtomicAndPaged(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "hist")
	ctx := context.Background()
	if _, err := s.CreateSession(ctx, sampleRecord("hist")); err != nil {
		t.Fatal(err)
	}
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native-1", ConfigDir: "/provider/home", CWD: "/original/folder"}
	rec.Metadata.ProviderConversationID = "native-1"
	messages := make([]sessionimport.Message, 205)
	for i := range messages {
		messages[i] = sessionimport.Message{Role: domain.MessageRoleUser, Text: fmt.Sprintf("message-%d", i+1), At: time.Now()}
	}
	imported, fresh, err := s.CreateImportedSession(ctx, rec, messages)
	if err != nil || !fresh {
		t.Fatalf("create: %v %v", fresh, err)
	}
	again, fresh, err := s.CreateImportedSession(ctx, rec, messages)
	if err != nil || fresh || again.ID != imported.ID {
		t.Fatalf("repeat: %v %v", again, err)
	}
	loaded, found, err := s.GetSession(ctx, imported.ID)
	if err != nil || !found || !loaded.NeedsImportResume() || loaded.Metadata.ImportSource.CWD != "/original/folder" {
		t.Fatalf("reload: %v %v", loaded, err)
	}
	page, more, err := s.LoadSessionImportMessages(ctx, imported.ID, 0, 100)
	if err != nil || !more || len(page) != 100 || page[0].Sequence != 106 || page[99].Sequence != 205 {
		t.Fatalf("last page: %v %v", page, err)
	}
	page, more, err = s.LoadSessionImportMessages(ctx, imported.ID, 106, 200)
	if err != nil || more || len(page) != 105 || page[0].Text != "message-1" {
		t.Fatalf("first page: %v %v", page, err)
	}
	rec.Metadata.ImportSource.NativeID = "bad-import"
	if _, _, err := s.CreateImportedSession(ctx, rec, []sessionimport.Message{{Role: "tool", Text: "invalid", At: time.Now()}}); err == nil {
		t.Fatal("invalid archive accepted")
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("partial row persisted: %v %v", all, err)
	}
}

func TestImportedWorkspacePublicationKeepsConcurrentSessionEdits(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rec := domain.SessionRecord{ProjectID: "", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native", ConfigDir: "/codex"}
	created, _, err := s.CreateImportedSession(ctx, rec, []sessionimport.Message{{Role: domain.MessageRoleUser, Text: "hello", At: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	renamed := created
	renamed.DisplayName = "renamed during copy"
	if err := s.UpdateSession(ctx, renamed); err != nil {
		t.Fatal(err)
	}
	next := *created.Metadata.ImportSource
	next.Prepared = true
	ok, err := s.SetSessionImportWorkspace(ctx, created.ID, created.Metadata.ImportSource, &next, "ao/import", "/workspace", "/repo", time.Now())
	if err != nil || !ok {
		t.Fatalf("publish: %v %v", ok, err)
	}
	got, _, _ := s.GetSession(ctx, created.ID)
	if got.DisplayName != renamed.DisplayName || !got.Metadata.ImportSource.Prepared {
		t.Fatal("publication reverted concurrent session edits")
	}
	got.IsTerminated = true
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetSessionImportWorkspace(ctx, got.ID, &next, &next, "ao/import", "/workspace", "/repo", time.Now()); err != nil || ok {
		t.Fatalf("published onto terminated session: %v %v", ok, err)
	}
}

// The archive owns the first sequences; live rows follow it, so a refresh is
// only accepted until the conversation exists.
func TestImportedConversationFollowsArchive(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "hist")
	ctx := context.Background()
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native-1", ConfigDir: "/provider/home"}
	rec.Metadata.ProviderConversationID = "native-1"
	message := func(text string) sessionimport.Message {
		return sessionimport.Message{Role: domain.MessageRoleUser, Text: text, At: time.Now()}
	}
	imported, _, err := s.CreateImportedSession(ctx, rec, []sessionimport.Message{message("one"), message("two")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceImportedMessages(ctx, imported.ID, []sessionimport.Message{message("one"), message("two"), message("three")}); err != nil {
		t.Fatal(err)
	}
	conversation, err := s.OpenNativeConversation(ctx, "conv-hist", domain.ConversationScopeSession, "hist", imported.ID, time.Now())
	if err != nil || conversation.LatestSequence != 3 {
		t.Fatalf("conversation = %+v: %v", conversation, err)
	}
	if err := s.ReplaceImportedMessages(ctx, imported.ID, []sessionimport.Message{message("late")}); err != nil {
		t.Fatal(err)
	}
	page, _, err := s.LoadSessionImportMessages(ctx, imported.ID, 0, 10)
	if err != nil || len(page) != 3 || page[2].Text != "three" {
		t.Fatalf("archive after the conversation opened = %+v: %v", page, err)
	}
}

// Full-row writes come from snapshots that may predate a workspace transfer or
// adoption; neither may be undone, and old rows must still match their source.
func TestImportFactsSurviveOlderSessionWrites(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+failureStorePragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rec := domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native", ConfigDir: "/codex"}
	created, _, err := s.CreateImportedSession(ctx, rec, []sessionimport.Message{{Role: domain.MessageRoleUser, Text: "hello", At: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	// A row written before "transferred" and "prepared" existed still matches.
	if _, err := db.Exec(`UPDATE sessions SET import_source = '{"nativeId":"native","configDir":"/codex","cwd":"","adopted":false}' WHERE id = ?`, created.ID); err != nil {
		t.Fatal(err)
	}
	older := created
	older.DisplayName = "written from an older snapshot"
	transferred := *created.Metadata.ImportSource
	transferred.Transferred = true
	if ok, err := s.SetSessionImportWorkspace(ctx, created.ID, created.Metadata.ImportSource, &transferred, "ao/import", "/workspace", "/repo", time.Now()); err != nil || !ok {
		t.Fatalf("transfer: %v %v", ok, err)
	}
	if err := s.UpdateSession(ctx, older); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetSession(ctx, created.ID)
	if got.DisplayName != older.DisplayName || !got.Metadata.ImportSource.Transferred {
		t.Fatalf("older snapshot undid the transfer: %+v", got.Metadata.ImportSource)
	}
	adopted := got
	adopted.Metadata.ImportSource.Adopted = true
	adopted.Metadata.ImportSource.HistoryThrough = 7
	if err := s.UpdateSession(ctx, adopted); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSession(ctx, older); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetSession(ctx, created.ID)
	if source := got.Metadata.ImportSource; !source.Adopted || source.HistoryThrough != 7 || !source.Transferred {
		t.Fatalf("older snapshot undid adoption: %+v", source)
	}
	// An agent switch changes the harness, not the conversation it came from.
	got.Harness = domain.HarnessClaudeCode
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, fresh, err := s.CreateImportedSession(ctx, rec, []sessionimport.Message{{Role: domain.MessageRoleUser, Text: "hello", At: time.Now()}})
	if err != nil || fresh || again.ID != created.ID {
		t.Fatalf("switched session imported again: %v %v %v", again.ID, fresh, err)
	}
}
