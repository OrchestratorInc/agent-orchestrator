package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// terminalTurnsFailStore makes the replacement note's turn read fail while
// keeping the concrete store, whose ProjectConversation the note relies on.
type terminalTurnsFailStore struct{ *sqlite.Store }

func (terminalTurnsFailStore) ListTerminalTurns(context.Context, domain.SessionID) ([]domain.HandoffTurn, error) {
	return nil, errors.New("terminal turns unavailable")
}

// seedPreviousOrchestrator runs an orchestrator through one completed chat
// turn on the project conversation, then terminates it, as a replacement
// finds it.
func seedPreviousOrchestrator(t *testing.T, st *sqlite.Store, task string) {
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
func startReplacement(t *testing.T, store chatsvc.Store, st *sqlite.Store, id domain.SessionID, dataDir, idPrefix string, commitBoundary bool) ports.ChatStartConfig {
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
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), DataDir: dataDir,
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

	started := startReplacement(t, st, st, id, "", "replacement", true)

	for _, want := range []string{domain.HandoffNotePrefix, "migrate the billing service to the new ledger", "ledger migration plan agreed"} {
		if !strings.Contains(started.SystemPrompt, want) {
			t.Fatalf("replacement SystemPrompt = %q, want it to contain %q", started.SystemPrompt, want)
		}
	}
}

func TestReplacementOrchestratorKeepsItsHandoffNoteAcrossRestarts(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")
	dataDir := t.TempDir()

	startReplacement(t, st, st, id, dataDir, "replacement", true)
	files, err := filepath.Glob(filepath.Join(dataDir, "handoffs", "*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("persisted notes = %v, err=%v; want one file", files, err)
	}
	if body, err := os.ReadFile(files[0]); err != nil || !strings.Contains(string(body), "migrate the billing service to the new ledger") {
		t.Fatalf("persisted note = %q, err=%v", body, err)
	}

	restarted := startReplacement(t, st, st, id, dataDir, "restarted", false)
	if !strings.Contains(restarted.SystemPrompt, "migrate the billing service to the new ledger") {
		t.Fatalf("restarted SystemPrompt = %q, want the persisted handoff note", restarted.SystemPrompt)
	}
}

func TestFirstOrchestratorStartsWithoutHandoffNote(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	dataDir := t.TempDir()

	started := startReplacement(t, st, st, id, dataDir, "first", true)

	if strings.Contains(started.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("first orchestrator SystemPrompt = %q, want no handoff note", started.SystemPrompt)
	}
	if files, _ := filepath.Glob(filepath.Join(dataDir, "handoffs", "*")); len(files) != 0 {
		t.Fatalf("first orchestrator persisted %v, want none", files)
	}
}

func TestReplacementHandoffNoteFailureDoesNotFailTheStart(t *testing.T) {
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	provisioningOrchestrator(t, st, id)
	seedPreviousOrchestrator(t, st, "migrate the billing service to the new ledger")

	started := startReplacement(t, terminalTurnsFailStore{st}, st, id, t.TempDir(), "replacement", true)

	if strings.Contains(started.SystemPrompt, domain.HandoffNotePrefix) {
		t.Fatalf("SystemPrompt = %q, want no note when it cannot be built", started.SystemPrompt)
	}
}
