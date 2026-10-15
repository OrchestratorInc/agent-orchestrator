package chat_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestImportedResumeRejectsEmptyNativeReplay(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Now()
	rec, _, err := st.CreateImportedSession(ctx, domain.SessionRecord{
		Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: now, UpdatedAt: now,
		Metadata: domain.SessionMetadata{ProviderConversationID: "thread-1",
			ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider"}},
	}, []sessionimport.Message{{Role: domain.MessageRoleUser, Text: "saved history", At: now}})
	if err != nil {
		t.Fatal(err)
	}
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: &nativeHistoryConversation{fakeConversation: newFakeConversation()}}},
		NewID:   func() string { nextID++; return fmt.Sprintf("import-empty-%d", nextID) }})
	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: rec.ID, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnavailable) || svc.HasLiveChatController(rec.ID) {
		t.Fatalf("empty replay acquired a controller: %v", err)
	}
	snapshot, err := svc.Snapshot(ctx, rec.ID)
	if err != nil || len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "saved history" {
		t.Fatalf("empty replay replaced the archive: %+v %v", snapshot, err)
	}
}

func TestImportedSnapshotSurvivesProviderSourceDeletion(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	now := time.Now()
	transcript := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(transcript, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	rec, _, err := st.CreateImportedSession(ctx, domain.SessionRecord{Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now, Metadata: domain.SessionMetadata{NativeTranscriptPath: transcript, ImportSource: &domain.SessionImportSource{NativeID: "native-1", ConfigDir: "/provider/home"}}}, []sessionimport.Message{{Role: domain.MessageRoleUser, Text: "first", At: now}, {Role: domain.MessageRoleAssistant, Text: "last", At: now}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st})
	snapshot, err := svc.SnapshotPage(ctx, rec.ID, 0, 1)
	if err != nil || len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "last" || !snapshot.HasMoreBefore || snapshot.Controller != ports.ChatControllerStopped {
		t.Fatalf("saved snapshot: %#v %v", snapshot, err)
	}
	older, err := svc.SnapshotPage(ctx, rec.ID, snapshot.OldestSequence, 1)
	if err != nil || len(older.Messages) != 1 || older.Messages[0].Text != "first" || older.HasMoreBefore {
		t.Fatalf("older snapshot: %#v %v", older, err)
	}
}
