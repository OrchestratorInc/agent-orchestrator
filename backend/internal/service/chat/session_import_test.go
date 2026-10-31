package chat_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Claude resumes from its latest compaction, so its replay is only the tail of
// an imported conversation. The archive stays the visible history below AO turns.
func TestAdoptedImportShowsArchiveInPlaceOfPartialReplay(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Truncate(time.Second)
	rec, _, err := st.CreateImportedSession(ctx, domain.SessionRecord{
		Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle}, CreatedAt: old, UpdatedAt: old,
		Metadata: domain.SessionMetadata{ProviderConversationID: "thread-1",
			ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider"}},
	}, []sessionimport.Message{
		{Role: domain.MessageRoleUser, Text: "first", At: old}, {Role: domain.MessageRoleAssistant, Text: "reply one", At: old},
		{Role: domain.MessageRoleUser, Text: "second", At: old}, {Role: domain.MessageRoleAssistant, Text: "reply two", At: old},
	})
	if err != nil {
		t.Fatal(err)
	}
	conv := &nativeHistoryConversation{fakeConversation: newFakeConversation(), events: []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "tail-start", ProviderTurnID: "native-2"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "tail-user", ProviderTurnID: "native-2", ProviderItemID: "item-1", Text: "second"},
		{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "tail-answer", ProviderTurnID: "native-2", ProviderItemID: "item-2", Text: "reply two"},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "tail-done", ProviderTurnID: "native-2", TurnState: domain.TurnStateCompleted},
	}}
	conv.providerConversationID = "thread-1"
	nextID := 0
	pages := chatsvc.SnapshotPageReaderFunc(func(ctx context.Context, id string, before, limit int64) (chatsvc.ConversationRows, error) {
		rows, err := st.LoadConversationSnapshotPage(ctx, id, before, limit)
		return chatsvc.ConversationRows{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns,
			Messages: rows.Messages, Activities: rows.Activities, OldestSequence: rows.OldestSequence, HasMoreBefore: rows.HasMoreBefore}, err
	})
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Reader: fullSnapshotReader(st), PageReader: pages,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		NewID:   func() string { nextID++; return fmt.Sprintf("import-tail-%d", nextID) }})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), rec.ID) })
	controller, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: rec.ID, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := st.ConversationForSession(ctx, rec.ID)
	if err != nil || conversation.LatestSequence <= 4 {
		t.Fatalf("replay did not follow the archive's sequences: %+v %v", conversation, err)
	}
	// Session Manager records the replay boundary when it adopts the import.
	adopted, _, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	adopted.Metadata.ImportSource.Adopted = true
	adopted.Metadata.ImportSource.HistoryThrough = conversation.LatestSequence
	if err := st.UpdateSession(ctx, adopted); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Send(ctx, ports.ChatUserMessage{Text: "third", ClientMessageID: "after-adoption"}); err != nil {
		t.Fatal(err)
	}

	texts := func(messages []domain.ConversationMessage) string {
		var out []string
		for _, message := range messages {
			out = append(out, message.Text)
		}
		return strings.Join(out, ",")
	}
	page, err := svc.SnapshotPage(ctx, rec.ID, 0, 100)
	if err != nil || texts(page.Messages) != "first,reply one,second,reply two,third" || page.HasMoreBefore {
		t.Fatalf("timeline = %q more=%v: %v", texts(page.Messages), page.HasMoreBefore, err)
	}
	if !page.Messages[0].CreatedAt.Equal(old) {
		t.Fatalf("archived message time = %v, want %v", page.Messages[0].CreatedAt, old)
	}
	newest, err := svc.SnapshotPage(ctx, rec.ID, 0, 3)
	if err != nil || texts(newest.Messages) != "second,reply two,third" || !newest.HasMoreBefore {
		t.Fatalf("newest page = %q more=%v: %v", texts(newest.Messages), newest.HasMoreBefore, err)
	}
	older, err := svc.SnapshotPage(ctx, rec.ID, newest.OldestSequence, 3)
	if err != nil || texts(older.Messages) != "first,reply one" || older.HasMoreBefore {
		t.Fatalf("older page = %q more=%v: %v", texts(older.Messages), older.HasMoreBefore, err)
	}
}
