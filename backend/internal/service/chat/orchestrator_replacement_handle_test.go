package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// A replacement orchestrator that is still provisioning when it starts takes the
// first-controller path. The project conversation it inherits holds the previous
// orchestrator's provider handle, so its own handle has to be published to the
// active branch, or every later resume is rejected as a handle mismatch.
func TestReplacementOrchestratorPublishesItsProviderHandleToTheInheritedBranch(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	before, sourceBranch := seedProjectConversationWithProviderHistory(
		t, st, "replacement-conversation", now)
	if sourceBranch.ProviderConversationID != "source-provider-thread" || sourceBranch.SessionID != testSession {
		t.Fatalf("seeded source branch = %+v", sourceBranch)
	}
	replacement, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("create replacement session: %v", err)
	}

	fresh := newFakeConversation()
	fresh.providerConversationID = "replacement-provider-thread"
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: fresh}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			nextID++
			return "replacement-" + string(rune('a'+nextID))
		},
		Now: func() time.Time { return now.Add(2 * time.Second) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), replacement.ID) })

	// What the user types while the replacement provisions waits in a queue.
	if _, err := svc.Send(ctx, replacement.ID, ports.ChatUserMessage{
		Text: "carry on", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue opening prompt: %v", err)
	}

	lifecycleManager := lifecycle.New(st, nil)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			metadata := domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID,
				ControllerGeneration:   result.ControllerGeneration,
			}
			committed := result.Conversation
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{Conversation: committed},
					lifecycleManager.MarkSpawned(ctx, replacement.ID, metadata)
			}
			if err := lifecycleManager.MarkChatSpawned(ctx, replacement.ID, metadata, *result.ProviderBoundary); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	}); err != nil {
		t.Fatalf("start replacement orchestrator: %v", err)
	}
	if err := svc.Stop(ctx, replacement.ID); err != nil {
		t.Fatalf("stop replacement controller: %v", err)
	}

	record, found, err := st.GetSession(ctx, replacement.ID)
	if err != nil || !found {
		t.Fatalf("read replacement: found=%v err=%v", found, err)
	}
	conversation, err := st.ConversationForSession(ctx, replacement.ID)
	if err != nil {
		t.Fatalf("conversation for replacement: %v", err)
	}
	active, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatalf("active branch: %v", err)
	}
	t.Logf("before: active=%s  after: active=%s branch{session=%s handle=%q} session handle=%q",
		before.ActiveBranchID, active.ID, active.SessionID, active.ProviderConversationID,
		record.Metadata.ProviderConversationID)
	if active.ProviderConversationID != record.Metadata.ProviderConversationID {
		t.Fatalf("active branch handle = %q (session %s), session handle = %q: the replacement's handle was never published",
			active.ProviderConversationID, active.SessionID, record.Metadata.ProviderConversationID)
	}

	// The daemon restarts and the session resumes with its own handle.
	resumed := newFakeConversation()
	resumed.providerConversationID = record.Metadata.ProviderConversationID
	restarted := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: resumed}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "replacement-resume-generation" },
		Now:     func() time.Time { return now.Add(3 * time.Second) },
	})
	t.Cleanup(func() { _ = restarted.Stop(context.Background(), replacement.ID) })
	if _, err := restarted.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ProviderConversationID: record.Metadata.ProviderConversationID,
		HistoryMode:            ports.ChatHistoryDeferred,
	}); err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
}

func replacementService(st chatsvc.Store, sessions chatsvc.SessionReader, reader chatsvc.SnapshotReader, driver fakeDriver, now time.Time) *chatsvc.Service {
	nextID := 0
	return chatsvc.New(chatsvc.Options{
		Store: st, Sessions: sessions, Reader: reader,
		Drivers: fakeRegistry{driver: driver},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			nextID++
			return "edge-" + string(rune('a'+nextID))
		},
		Now: func() time.Time { return now },
	})
}

// The reserved boundary must not cost the replacement its opening prompt: the
// queued turn is still there to drain and was not settled as orphaned work.
func TestReplacementOrchestratorKeepsItsQueuedOpeningPrompt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	seedProjectConversationWithProviderHistory(t, st, "queued-replacement-conversation", now)
	replacement, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("create replacement session: %v", err)
	}
	fresh := newFakeConversation()
	fresh.providerConversationID = "replacement-provider-thread"
	svc := replacementService(st, st, fullSnapshotReader(st), fakeDriver{conv: fresh}, now.Add(2*time.Second))
	t.Cleanup(func() { _ = svc.Stop(context.Background(), replacement.ID) })
	if _, err := svc.Send(ctx, replacement.ID, ports.ChatUserMessage{
		Text: "the opening brief", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue opening prompt: %v", err)
	}
	lifecycleManager := lifecycle.New(st, nil)
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, errors.New("inherited handle was not fenced by a boundary")
			}
			err := lifecycleManager.MarkChatSpawned(ctx, replacement.ID, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID,
				ControllerGeneration:   result.ControllerGeneration,
			}, *result.ProviderBoundary)
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, err
		},
	}); err != nil {
		t.Fatalf("start replacement: %v", err)
	}
	snapshot, err := svc.Snapshot(ctx, replacement.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Turns) != 1 {
		t.Fatalf("turns = %d, want the queued opening prompt", len(snapshot.Turns))
	}
	if state := snapshot.Turns[0].State; state == domain.TurnStateFailed {
		t.Fatalf("opening prompt was settled as orphaned work: %q", snapshot.Turns[0].ErrorMessage)
	}
}

// An orchestrator that inherits a conversation nobody has written to has no
// provider history to fence off, so it still starts on the same root.
func TestReplacementOrchestratorOfUntouchedConversationStillStartsOnTheRoot(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	record, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	record.Metadata.ProviderConversationID = "source-provider-thread"
	if err := st.UpdateSession(ctx, record); err != nil {
		t.Fatalf("seed source handle: %v", err)
	}
	if _, err := st.CreateConversation(ctx, "untouched-conversation",
		domain.ConversationScopeProject, testProject, testSession, now); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	replacement, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("create replacement session: %v", err)
	}
	fresh := newFakeConversation()
	fresh.providerConversationID = "replacement-provider-thread"
	svc := replacementService(st, st, fullSnapshotReader(st), fakeDriver{conv: fresh}, now.Add(2*time.Second))
	t.Cleanup(func() { _ = svc.Stop(context.Background(), replacement.ID) })
	if _, err := svc.Send(ctx, replacement.ID, ports.ChatUserMessage{
		Text: "hello", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue opening prompt: %v", err)
	}
	boundaryReserved := false
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			boundaryReserved = result.ProviderBoundary != nil
			return chatsvc.ControllerCommit{Conversation: result.Conversation}, nil
		},
	}); err != nil {
		t.Fatalf("start replacement: %v", err)
	}
	if boundaryReserved {
		t.Fatal("an untouched inherited conversation was fenced as provider history")
	}
}

// A failed provider start must leave the inherited head and owner alone, and a
// later start must still be able to publish the replacement's handle.
func TestReplacementOrchestratorRetriesAfterAFailedProviderStart(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	before, _ := seedProjectConversationWithProviderHistory(t, st, "retry-conversation", now)
	replacement, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, ProvisionState: domain.SessionProvisionProvisioning,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("create replacement session: %v", err)
	}
	providerErr := errors.New("provider unavailable")
	failing := replacementService(st, st, fullSnapshotReader(st), fakeDriver{
		start: func(ports.ChatStartConfig) (ports.ChatConversation, error) { return nil, providerErr },
	}, now.Add(2*time.Second))
	if _, err := failing.Send(ctx, replacement.ID, ports.ChatUserMessage{
		Text: "brief", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("queue opening prompt: %v", err)
	}
	if _, err := failing.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
	}); !errors.Is(err, providerErr) {
		t.Fatalf("failed start = %v, want provider failure", err)
	}
	after, err := st.ConversationForSession(ctx, testSession)
	if err == nil && after.ActiveBranchID != before.ActiveBranchID {
		t.Fatalf("failed start moved the head from %q to %q", before.ActiveBranchID, after.ActiveBranchID)
	}

	fresh := newFakeConversation()
	fresh.providerConversationID = "replacement-provider-thread"
	retry := replacementService(st, st, fullSnapshotReader(st), fakeDriver{conv: fresh}, now.Add(3*time.Second))
	t.Cleanup(func() { _ = retry.Stop(context.Background(), replacement.ID) })
	lifecycleManager := lifecycle.New(st, nil)
	if _, err := retry.Start(ctx, chatsvc.StartConfig{
		SessionID: replacement.ID, ProjectID: testProject, Kind: domain.KindOrchestrator,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if result.ProviderBoundary == nil {
				return chatsvc.ControllerCommit{}, errors.New("retry did not reserve a boundary")
			}
			err := lifecycleManager.MarkChatSpawned(ctx, replacement.ID, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID,
				ControllerGeneration:   result.ControllerGeneration,
			}, *result.ProviderBoundary)
			committed := result.Conversation
			committed.ActiveBranchID = result.ProviderBoundary.ID
			return chatsvc.ControllerCommit{Conversation: committed}, err
		},
	}); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	conversation, err := st.ConversationForSession(ctx, replacement.ID)
	if err != nil {
		t.Fatalf("conversation after retry: %v", err)
	}
	active, err := st.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		t.Fatalf("active branch after retry: %v", err)
	}
	if active.SessionID != replacement.ID || active.ProviderConversationID != "replacement-provider-thread" {
		t.Fatalf("active branch after retry = %+v, want the replacement's own", active)
	}
}
