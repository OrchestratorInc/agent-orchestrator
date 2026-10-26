package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestActivityUsesSingleRowReaderAndSessionGuards(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, ActivityReader: st, Reader: chatsvc.SnapshotReaderFunc(func(context.Context, string) (chatsvc.ConversationRows, error) {
		t.Fatal("single activity read loaded whole snapshot")
		return chatsvc.ConversationRows{}, nil
	})})
	if _, err := svc.Activity(ctx, "missing", "row"); !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("missing session err=%v", err)
	}
	if _, err := svc.Activity(ctx, testSession, "row"); !errors.Is(err, domain.ErrConversationActivityNotFound) {
		t.Fatalf("no conversation err=%v", err)
	}
	tui, err := st.CreateSession(ctx, domain.SessionRecord{ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Activity(ctx, tui.ID, "row"); !errors.Is(err, chatsvc.ErrNotChatMode) {
		t.Fatalf("TUI session err=%v", err)
	}
	conversation, err := st.CreateConversation(ctx, "activity-conversation", domain.ConversationScopeProject, testProject, testSession, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	detail := []byte(`{"output":"` + strings.Repeat("whole", 1000) + `"}`)
	if err := st.UpsertActivity(ctx, conversation.ID, "", domain.ConversationActivity{ID: "row", ProviderItemID: "provider-row", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Detail: detail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	row, err := svc.Activity(ctx, testSession, "row")
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != "row" || string(row.Detail) != string(detail) {
		t.Fatal("activity reader did not return full row")
	}
	if _, err := svc.Activity(ctx, testSession, "missing"); !errors.Is(err, domain.ErrConversationActivityNotFound) {
		t.Fatalf("missing activity err=%v", err)
	}
}
