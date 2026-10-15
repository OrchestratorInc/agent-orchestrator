package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
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
