package sessionmanager

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type accountSwitchChatLauncher struct {
	integrationChatLauncher
	startError *error
}

func (c accountSwitchChatLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	started, err := c.service.StartChat(ctx, cfg)
	*c.startError = err
	return started, err
}

func (c accountSwitchChatLauncher) ArmAccountsManagerHandoff(ctx context.Context, id domain.SessionID, fresh bool) error {
	return c.service.ArmAccountsManagerHandoff(ctx, id, fresh)
}
func (c accountSwitchChatLauncher) PrepareAccountsManagerHandoff(ctx context.Context, id domain.SessionID, policy domain.SessionInterfaceTransitionPolicy) error {
	return c.service.PrepareAccountsManagerHandoff(ctx, id, policy)
}
func (c accountSwitchChatLauncher) AbortAccountsManagerHandoff(id domain.SessionID) {
	c.service.AbortAccountsManagerHandoff(id)
}
func (c accountSwitchChatLauncher) AcknowledgeAccountsManagerSwitch(ctx context.Context, id domain.SessionID, generation string, commit func(context.Context) error) error {
	return c.service.AcknowledgeAccountsManagerSwitch(ctx, id, generation, commit)
}
func (c accountSwitchChatLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return c.service.DrainQueued(ctx, id)
}

func (r *accountSwitchRouter) AgentAccountSwitchPending(ctx context.Context, id domain.SessionID) (bool, error) {
	op, found, err := r.store.GetLatestAccountsManagerSwitch(ctx, id)
	return found && !op.Phase.Terminal(), err
}

type accountSwitchHistoryConversation struct{ *integrationChatConversation }

func (c accountSwitchHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderConversationID: c.providerID, ProviderTurnID: "provider-turn-1", ProviderEventID: "source-start"},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderConversationID: c.providerID, ProviderTurnID: "provider-turn-1", ProviderItemID: "source-message", ProviderEventID: "source-message-event", Text: "running"},
		{Kind: ports.ChatEventTurnCompleted, ProviderConversationID: c.providerID, ProviderTurnID: "provider-turn-1", ProviderEventID: "source-complete", TurnState: domain.TurnStateCompleted},
	}, nil
}

func TestAccountsManagerSwitchChatAdoptsPausedQueueAfterReadiness(t *testing.T) {
	m, st, _, _, rec, cfg := accountSwitchFixtureMode(t, domain.SessionModeChat)
	rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID = "", ""
	rec.Metadata.ControllerGeneration, rec.Metadata.ProviderConversationID = "chat-source", "same-history"
	rec.Metadata.Prompt = "running"
	if err := st.UpdateSession(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	m.agents = singleAgent{agent: transitionAgent{}}
	source := newIntegrationChatConversation(rec.Metadata.ProviderConversationID)
	target := newIntegrationChatConversation(rec.Metadata.ProviderConversationID)
	// Provider turn IDs remain unique when resuming the same history.
	target.sent = []ports.ChatUserMessage{{Text: "already-dispatched"}}
	starts := 0
	driver := integrationChatDriver{harness: rec.Harness, start: func() ports.ChatConversation { t.Error("switch unexpectedly started a new history"); return target }, resume: func() ports.ChatConversation {
		starts++
		if starts == 1 {
			return source
		}
		return accountSwitchHistoryConversation{target}
	}}
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: integrationChatRegistry{rec.Harness: driver}, AccountsManager: m.accountsManager,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, id string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, id)
			return chatsvc.ConversationRows{Conversation: rows.Conversation, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities, BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage}, err
		}),
		NewID: uuid.NewString, Now: time.Now, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { svc.StopAll(context.Background()) })
	if _, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: rec.ID, ProjectID: rec.ProjectID, Harness: rec.Harness, WorkspacePath: rec.Metadata.WorkspacePath,
		ProviderConversationID: rec.Metadata.ProviderConversationID, ControllerGeneration: rec.Metadata.ControllerGeneration, HistoryMode: ports.ChatHistoryDeferred}); err != nil {
		t.Fatal(err)
	}
	var startError error
	m.chat = accountSwitchChatLauncher{integrationChatLauncher{service: svc}, &startError}
	for _, text := range []string{"running", "queued"} {
		if _, err := svc.Send(t.Context(), rec.ID, ports.ChatUserMessage{Text: text, ClientMessageID: text}); err != nil {
			t.Fatal(err)
		}
	}
	cfg.NewConversation, cfg.Policy = false, domain.SessionInterfaceTransitionDrain
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatal(err)
	}
	select {
	case source.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted}:
	case <-time.After(3 * time.Second):
		t.Fatal("source projector did not finish the active turn")
	}
	op := waitAccountSwitch(t, m, st, cfg.OperationID)
	if op.Phase != domain.AccountsManagerSwitchReady {
		t.Fatalf("Chat switch did not acknowledge: phase=%s code=%s launch=%v", op.Phase, op.ErrorCode, startError)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		target.mu.Lock()
		sent := append([]ports.ChatUserMessage(nil), target.sent...)
		target.mu.Unlock()
		if len(sent) == 2 {
			if sent[1].Text != "queued" {
				t.Fatal("switch replayed the task instead of adopting the queue")
			}
			source.mu.Lock()
			defer source.mu.Unlock()
			if len(source.sent) != 1 {
				t.Fatal("queued work used the source account")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("acknowledged target did not adopt queued work")
}
