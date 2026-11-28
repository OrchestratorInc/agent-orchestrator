package chat_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/chat"
)

// An ACP agent without message ids (agy) numbers replay turns by position. An
// earlier import matched replay turn user-2 to an existing AO turn and archived
// its turn.started under user-2's event id. A later replay no longer matched it,
// so its turn.started was deduplicated and never adopted, and its user message
// named a turn AO never recorded. That failed every resume of the session
// (agent-orchestrator-222). The resume must succeed without inventing a turn.
func TestResumeSkipsUserMessageOfReplayTurnAlreadyArchivedElsewhere(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 11, 23, 4, 40, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(ctx, "conversation-222",
		domain.ConversationScopeSession, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, testSession, "old-generation"); err != nil {
		t.Fatalf("ClaimChatControllerGeneration: %v", err)
	}
	if created, err := st.AppendUserMessage(ctx, conversation.ID, testSession, "old-generation",
		domain.ConversationMessage{ID: "prompt", Text: "check the claim", Origin: domain.MessageOriginHuman, ClientMessageID: "client-prompt"},
		"ao-turn", now); err != nil || !created {
		t.Fatalf("AppendUserMessage: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "ao-turn", "live-turn", now); err != nil {
		t.Fatalf("BindTurnToProvider: %v", err)
	}
	if err := st.SettleAssistantMessage(ctx, conversation.ID, "msg-1", "live-turn", "Mostly true.", "answer", now); err != nil {
		t.Fatalf("SettleAssistantMessage: %v", err)
	}
	if err := st.SettleTurn(ctx, conversation.ID, "live-turn", domain.TurnStateCompleted, "", now); err != nil {
		t.Fatalf("SettleTurn: %v", err)
	}
	// The earlier import: user-2's start was projected onto live-turn and its
	// user message was dropped as a duplicate, so only the start is archived.
	if _, err := st.ProjectProviderEvent(ctx, conversation.ID, testSession, "old-generation",
		"user-2:started", string(ports.ChatEventTurnStarted), `{"providerTurnId":"live-turn"}`, now,
		func(context.Context) error { return nil }); err != nil {
		t.Fatalf("archive earlier import: %v", err)
	}

	replay := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "user-1:started", ProviderTurnID: "user-1"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "user-1:user", ProviderTurnID: "user-1",
			ProviderItemID: "user-1", ClientMessageID: "user-1", Text: "check the claim"},
		{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "user-1:answer", ProviderTurnID: "user-1",
			ProviderItemID: "msg-1", Text: "Mostly true."},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "user-1:completed", ProviderTurnID: "user-1",
			TurnState: domain.TurnStateRecovered},
		{Kind: ports.ChatEventTurnStarted, ProviderEventID: "user-2:started", ProviderTurnID: "user-2"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "user-2:user", ProviderTurnID: "user-2",
			ProviderItemID: "user-2", ClientMessageID: "user-2", Text: "and the follow-up"},
		{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "user-2:completed", ProviderTurnID: "user-2",
			TurnState: domain.TurnStateRecovered},
	}
	conv := &nativeHistoryConversation{fakeConversation: newFakeConversation(), events: replay}
	var ids atomic.Int64
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("resume-%d", ids.Add(1)) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryImport,
	}); err != nil {
		t.Fatalf("resume failed on a replay turn an earlier import archived elsewhere: %v", err)
	}

	rows, err := st.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(rows.Turns) != 1 || rows.Turns[0].ProviderTurnID != "live-turn" {
		t.Fatalf("turns = %+v, want only the original live-turn", rows.Turns)
	}
	for _, message := range rows.Messages {
		if message.Text == "and the follow-up" {
			t.Fatalf("skipped replay prompt was imported: %+v", message)
		}
	}
}
