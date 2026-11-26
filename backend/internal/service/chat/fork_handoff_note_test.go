package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// A text-handoff fork's agent gets the copied conversation as standing
// context, and keeps it when its controller restarts later.
func TestForkHandoffNoteStaysWithTheForkAcrossRestarts(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	conversation := newFakeConversation()
	var started ports.ChatStartConfig
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conversation, startCfg: &started, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "fork-handoff" },
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })

	workspace := t.TempDir()
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: workspace,
		SystemPrompt: "standing rules", ForkHandoffNote: "COPIED-CONVERSATION",
	}); err != nil {
		t.Fatalf("Start fork: %v", err)
	}
	if !strings.Contains(started.SystemPrompt, "standing rules") || !strings.Contains(started.SystemPrompt, "COPIED-CONVERSATION") {
		t.Fatalf("first start system prompt = %q, want the rules and the copied conversation", started.SystemPrompt)
	}

	if err := svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: workspace,
		SystemPrompt: "standing rules", ProviderConversationID: conversation.ProviderConversationID(),
	}); err != nil {
		t.Fatalf("restart fork: %v", err)
	}
	if !strings.Contains(resumed.SystemPrompt, "COPIED-CONVERSATION") {
		t.Fatalf("restart system prompt = %q, want the copied conversation kept", resumed.SystemPrompt)
	}
}

// An agent with no native fork is forked with its conversation copied as
// text, from its latest turn only.
func TestAnAgentThatCantForkIsForkedWithItsConversationAsText(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "remember the code word heron", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "answer", ProviderEventID: "answer", Text: "Noted: heron."},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderEventID: "done", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}

	source, err := h.svc.SessionForkSource(ctx, rec, "")
	if err != nil {
		t.Fatal(err)
	}
	if source.Refusal != "" || source.ContextMode != domain.ForkContextTextHandoff || source.LatestTurnID != turn.ID {
		t.Fatalf("source = %+v, want a text handoff from turn %s", source, turn.ID)
	}
	if source.AnyTurn || source.PrepareChat != nil || source.Handoff == nil {
		t.Fatalf("source = %+v, want latest turn only, no native fork, a handoff", source)
	}
	// A turn the original finishes after the fork was planned isn't the fork's.
	h.advance(time.Minute)
	later, err := h.ctrl.Send(ctx, ports.ChatUserMessage{Text: "a later question", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: later.ProviderTurnID},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: later.ProviderTurnID, ProviderItemID: "later", ProviderEventID: "later", Text: "a later answer"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: later.ProviderTurnID, ProviderEventID: "later-done", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[1].State == domain.TurnStateCompleted
	})
	note, err := source.Handoff(ctx, ports.ForkTarget{SessionID: "fork-1", WorkspacePath: "/forks/fork-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"remember the code word heron", "Noted: heron.", "forked from Orchestrator session " + string(testSession), "/forks/fork-1"} {
		if !strings.Contains(note, want) {
			t.Errorf("note is missing %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "a later question") {
		t.Errorf("note includes a turn finished after the fork was planned:\n%s", note)
	}

	older, err := h.svc.SessionForkSource(ctx, rec, "some-earlier-turn")
	if err != nil {
		t.Fatal(err)
	}
	if older.Refusal != sessionfork.CodeOlderTurn {
		t.Fatalf("older turn refusal = %q, want %q", older.Refusal, sessionfork.CodeOlderTurn)
	}
}

// Forking a fork that was given its conversation as text passes that
// conversation on: it lives in the fork's saved note, not its own turns.
func TestForkOfATextForkKeepsTheEarlierConversation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetConversationBranchHandoffNote(ctx, conversation.ID, conversation.ActiveBranchID, "INHERITED: the code word is heron"); err != nil {
		t.Fatal(err)
	}
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	note, err := h.svc.ForkHandoffNote(ctx, rec, ports.ForkTarget{SessionID: "fork-2", WorkspacePath: "/forks/fork-2"}, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "INHERITED: the code word is heron") {
		t.Fatalf("note lost the inherited conversation:\n%s", note)
	}
}

type failingBranchStore struct {
	*sqlite.Store
	fail *atomic.Bool
}

func (f failingBranchStore) ConversationBranch(ctx context.Context, conversationID, branchID string) (domain.ConversationBranch, error) {
	if f.fail.Load() {
		return domain.ConversationBranch{}, errors.New("database is locked")
	}
	return f.Store.ConversationBranch(ctx, conversationID, branchID)
}

// A fork whose source's earlier conversation can't be read must fail, not
// start without it.
func TestForkNoteFailsWhenTheEarlierConversationCannotBeRead(t *testing.T) {
	var fail atomic.Bool
	h := newHarnessWithConversationAndStore(t, nil, func(st *sqlite.Store) chatsvc.Store { return failingBranchStore{st, &fail} })
	ctx := context.Background()
	fail.Store(true)
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ForkHandoffNote(ctx, rec, ports.ForkTarget{SessionID: "fork-3"}, "", time.Time{}); err == nil {
		t.Fatal("want an error when the source's branch can't be read")
	}
}

// A fork's first start resumes the provider conversation copied for it, into
// a conversation row with no settings yet. It runs with the session's model,
// effort, and approval mode, and saves them, as a new conversation does.
func TestForkFirstStartKeepsTheSessionsSettings(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	var started ports.ChatStartConfig
	var resumed ports.ChatResumeConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), startCfg: &started, resumeCfg: &resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "fork-settings" },
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
		Model: "gpt-6.1", Effort: "high", Permissions: ports.PermissionMode("auto"), ForkFirstStart: true,
	}); err != nil {
		t.Fatalf("Start fork: %v", err)
	}
	if resumed.Model != "gpt-6.1" || resumed.Effort != "high" || resumed.Permissions != "auto" {
		t.Fatalf("resumed with %q / %q / %q, want the session's gpt-6.1 / high / auto", resumed.Model, resumed.Effort, resumed.Permissions)
	}
	conversation, err := st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if got := conversation.Settings; got.Model != "gpt-6.1" || got.ReasoningEffort != "high" || got.ApprovalMode != "auto" {
		t.Fatalf("conversation settings = %+v, want the session's", got)
	}
}
