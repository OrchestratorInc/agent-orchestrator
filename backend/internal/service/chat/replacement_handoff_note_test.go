package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// terminalTurnsFailStore makes the replacement note's turn read fail.
type terminalTurnsFailStore struct{ *sqlite.Store }

func (terminalTurnsFailStore) ListTerminalTurns(context.Context, domain.SessionID) ([]domain.HandoffTurn, error) {
	return nil, errors.New("terminal turns unavailable")
}

// seedPreviousOrchestrator runs an orchestrator through one completed chat
// turn on the project conversation, then terminates it, as a replacement
// finds it. A non-empty unanswered request is sent last and never answered,
// as when the orchestrator is replaced mid-turn.
func seedPreviousOrchestrator(t *testing.T, st *sqlite.Store, task string, unanswered ...string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	previous, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed earlier orchestrator: %v", err)
	}
	conv := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   sequentialIDs("previous"),
	})
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: previous.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("start earlier orchestrator: %v", err)
	}
	turn, err := svc.Send(ctx, previous.ID, ports.ChatUserMessage{Text: task, Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: turn.ProviderTurnID, ProviderItemID: "previous-answer", Text: "ledger migration plan agreed"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateCompleted},
	)
	waitForTurnState(t, svc, previous.ID, domain.TurnStateCompleted)
	for _, request := range unanswered {
		if _, err := svc.Send(ctx, previous.ID, ports.ChatUserMessage{Text: request, Origin: domain.MessageOriginHuman}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Stop(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	previous, _, err = st.GetSession(ctx, previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	previous.IsTerminated = true
	if err := st.UpdateSession(ctx, previous); err != nil {
		t.Fatal(err)
	}
}

// startReplacement starts the orchestrator id on a fresh service and returns
// what the provider was started with. The first start commits the provider
// boundary as spawn does.
// idPrefix must differ per start in one test: each start creates new rows.
func startReplacement(t *testing.T, store chatsvc.Store, st *sqlite.Store, id domain.SessionID, idPrefix string, commitBoundary bool) ports.ChatStartConfig {
	t.Helper()
	var started ports.ChatStartConfig
	svc := chatsvc.New(chatsvc.Options{
		Store: store, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: newFakeConversation(), startCfg: &started}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   sequentialIDs(idPrefix),
	})
	cfg := chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}
	if commitBoundary {
		cfg.ControllerReady = func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, nil
			}
			record, _, err := st.GetSession(context.Background(), id)
			if err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			record.ProvisionState = domain.SessionProvisionReady
			record.Metadata.ProviderConversationID = result.ProviderConversationID
			record.Metadata.ControllerGeneration = result.ControllerGeneration
			if err := st.CommitChatSpawn(context.Background(), record, *result.ProviderBoundary); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		}
	}
	if _, err := svc.Start(context.Background(), cfg); err != nil {
		t.Fatalf("start replacement orchestrator: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
	return started
}

func TestReplacementOrchestratorStartsWithPreviousOwnersHandoffNote(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")

	started := startReplacement(t, st, st, id, "replacement", true)

	for _, want := range []string{domain.HandoffNotePrefix, "migrate the billing service to the new ledger", "ledger migration plan agreed"} {
		if !strings.Contains(started.SystemPrompt, want) {
			t.Fatalf("replacement SystemPrompt = %q, want it to contain %q", started.SystemPrompt, want)
		}
	}
	if strings.Contains(started.SystemPrompt, "Unanswered request") {
		t.Fatalf("replacement SystemPrompt = %q, want no unanswered request after an idle replacement", started.SystemPrompt)
	}
}

func TestReplacementOrchestratorKeepsItsHandoffNoteAcrossRestarts(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")

	startReplacement(t, st, st, id, "replacement", true)
	// A fresh restart that commits its own boundary must carry the note on.
	restarted := startReplacement(t, st, st, id, "restarted", true)
	again := startReplacement(t, st, st, id, "again", false)

	for name, prompt := range map[string]string{"restart": restarted.SystemPrompt, "second restart": again.SystemPrompt} {
		if !strings.Contains(prompt, "migrate the billing service to the new ledger") {
			t.Fatalf("%s SystemPrompt = %q, want the replacement handoff note", name, prompt)
		}
	}
}

// A provider switch after the replacement gives the branch its own note; a cold
// resume of that branch must use it, not the older replacement note.
func TestReplacementNoteDoesNotOverrideALaterSwitchNote(t *testing.T) {
	ctx := context.Background()
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")
	startReplacement(t, st, st, id, "replacement", true)

	conversation, err := st.ConversationForSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := st.GetSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Metadata.ProviderConversationID = "switched-thread"
	record.Metadata.ControllerGeneration = "switched-generation"
	if err := st.CommitChatSpawn(ctx, record, domain.ConversationBranch{
		ID: "switch-branch", ConversationID: conversation.ID, SessionID: id,
		ProviderConversationID: "switched-thread", ParentBranchID: conversation.ActiveBranchID,
		ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "switch-branch", ProviderIDsScoped: true,
		Strategy: domain.ConversationBranchStrategyApproximateContext, ReplayCutoffSequence: conversation.LatestSequence,
		ReplayTruncated: true, HandoffNote: "switch note: plan agreed with the replacement", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("commit provider switch branch: %v", err)
	}

	resumed := startReplacement(t, st, st, id, "resumed", false)

	if !strings.Contains(resumed.SystemPrompt, "switch note: plan agreed with the replacement") ||
		strings.Contains(resumed.SystemPrompt, "migrate the billing service to the new ledger") {
		t.Fatalf("resumed SystemPrompt = %q, want only the switch branch's note", resumed.SystemPrompt)
	}
}

func TestReplacementMidTurnCarriesTheUnansweredRequest(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger", "use the staging account instead")

	started := startReplacement(t, st, st, id, "replacement", true)

	for _, want := range []string{"ledger migration plan agreed", "## Unanswered request", "use the staging account instead"} {
		if !strings.Contains(started.SystemPrompt, want) {
			t.Fatalf("replacement SystemPrompt = %q, want it to contain %q", started.SystemPrompt, want)
		}
	}
}

func TestFirstOrchestratorStartsWithoutHandoffNote(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)

	started := startReplacement(t, st, st, id, "first", true)

	if strings.Contains(started.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("first orchestrator SystemPrompt = %q, want no handoff note", started.SystemPrompt)
	}
}

func TestReplacementHandoffNoteFailureDoesNotFailTheStart(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")

	started := startReplacement(t, terminalTurnsFailStore{st}, st, id, "replacement", true)

	if strings.Contains(started.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("SystemPrompt = %q, want no note when it cannot be built", started.SystemPrompt)
	}
}
