package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// The stale identity a replacement orchestrator can inherit: the project
// conversation was already rebound to it, while the active NONROOT branch still
// records the terminated predecessor's provider handle. The provider threads
// below are fakes; no real provider state is read or written.
func seedReboundOrchestratorFixture(t *testing.T) historicalProviderFixture {
	t.Helper()
	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)

	source, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("load predecessor: found=%v err=%v", found, err)
	}
	source.Metadata.ProviderConversationID = "native-88"
	source.Metadata.ControllerGeneration = "generation-88"
	source.Metadata.WorkspacePath = "/worktrees/orchestrator-88"
	source.Metadata.Branch = "ao/orchestrator"
	source.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	source.UpdatedAt = now
	if err := st.UpdateSession(ctx, source); err != nil {
		t.Fatalf("seed predecessor: %v", err)
	}
	conversation, err := st.CreateConversation(ctx, "rebound-conversation",
		domain.ConversationScopeProject, testProject, source.ID, now)
	if err != nil {
		t.Fatalf("create project conversation: %v", err)
	}
	// The predecessor's own Chat epoch: an active NONROOT branch holding the
	// stale provider identity the replacement inherits.
	staleEpoch := domain.ConversationBranch{
		ID: "stale-epoch", ConversationID: conversation.ID, SessionID: source.ID,
		ProviderConversationID: "native-428", ParentBranchID: conversation.ActiveBranchID,
		ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "stale-epoch",
		ProviderIDsScoped: true, CreatedAt: now.Add(2 * time.Second),
	}
	if err := st.CreateAndActivateConversationBranch(ctx, source.ID, staleEpoch,
		"generation-88", now.Add(2*time.Second)); err != nil {
		t.Fatalf("seed stale epoch: %v", err)
	}
	if _, err := st.AppendUserMessage(ctx, conversation.ID, source.ID, "generation-88",
		domain.ConversationMessage{
			ID: "old-user-message", Text: "preserve the old orchestrator transcript",
			Origin: domain.MessageOriginHuman, ClientMessageID: "old-client-message",
		}, "old-turn", now.Add(3*time.Second)); err != nil {
		t.Fatalf("seed old transcript: %v", err)
	}
	source.IsTerminated = true
	source.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: now.Add(3 * time.Second)}
	if err := st.UpdateSession(ctx, source); err != nil {
		t.Fatalf("retire predecessor: %v", err)
	}
	target, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator,
		// Cross-harness on purpose: the replacement runs a different agent than
		// the predecessor whose branch identity it inherits.
		Harness: domain.HarnessQwen, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(4 * time.Second)},
		Metadata: domain.SessionMetadata{
			ProviderConversationID: historicalTargetThread,
			WorkspacePath:          "/worktrees/orchestrator-248",
			Branch:                 "ao/orchestrator",
		},
		CreatedAt: source.CreatedAt.Add(4 * time.Second), UpdatedAt: source.CreatedAt.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	conversation, err = st.CreateConversation(ctx, "unused-rebound",
		domain.ConversationScopeProject, testProject, target.ID, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("rebind project conversation: %v", err)
	}
	stale, err := st.ConversationBranch(ctx, conversation.ID, "stale-epoch")
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Active || stale.SessionID != source.ID || stale.ProviderConversationID != "native-428" {
		t.Fatalf("fixture stale epoch = %+v, want active predecessor identity", stale)
	}
	return historicalProviderFixture{
		store: st, conversation: conversation, root: stale, source: source, target: target, now: now,
	}
}

func reboundRecoveryHandoff(fixture historicalProviderFixture) *domain.ChatProviderHandoff {
	return &domain.ChatProviderHandoff{
		BoundaryID: "recovery-epoch", ConversationID: fixture.conversation.ID,
		PreviousSessionID: fixture.target.ID, PreviousBranchID: fixture.root.ID,
		PreviousSequence:        fixture.conversation.LatestSequence,
		ExpectedControllerOwner: fixture.target.ControllerOwner(),
		RecoverySource: &domain.ChatProviderHandoffRecoverySource{
			SessionID: fixture.source.ID, ProviderConversationID: "native-428",
		},
	}
}

func assertReboundFixtureUnchanged(t *testing.T, fixture historicalProviderFixture) {
	t.Helper()
	ctx := context.Background()
	record, found, err := fixture.store.GetSession(ctx, fixture.target.ID)
	if err != nil || !found {
		t.Fatalf("load replacement: found=%v err=%v", found, err)
	}
	if record.IsTerminated || record.Metadata.ProviderConversationID != historicalTargetThread ||
		record.Metadata.ControllerGeneration != fixture.target.Metadata.ControllerGeneration {
		t.Fatalf("failed recovery changed the replacement: %+v", record)
	}
	conversation, err := fixture.store.ConversationForSession(ctx, fixture.target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.ActiveBranchID != fixture.root.ID || conversation.SessionID != fixture.target.ID {
		t.Fatalf("failed recovery moved the head: %+v", conversation)
	}
	if _, err := fixture.store.ConversationBranch(ctx, conversation.ID, "recovery-epoch"); !errors.Is(err, domain.ErrNoConversationBranch) {
		t.Fatalf("failed recovery left a boundary: %v", err)
	}
	snapshot, err := fixture.store.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "preserve the old orchestrator transcript" {
		t.Fatalf("failed recovery changed the transcript: %+v", snapshot.Messages)
	}
	events, err := fixture.store.ProviderEventsSince(ctx, conversation.ID, 0, 20)
	if err != nil || len(events) != 0 {
		t.Fatalf("failed recovery committed provider events = %+v err=%v", events, err)
	}
}

// The whole ordinary-recovery regression: the coordinator-shaped handoff
// publishes the replacement's native thread atomically on a fresh boundary,
// leaves every predecessor row untouched, and every failure mode during
// provider I/O leaves durable state exactly as it was.
func TestReboundOrchestratorRecoveryPublication(t *testing.T) {
	scenarios := []string{
		"success", "provider_failure", "wrong_provider", "history_failure",
		"predecessor_revived", "competing_orchestrator", "head_moved", "history_changed",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			fixture := seedReboundOrchestratorFixture(t)
			handoff := reboundRecoveryHandoff(fixture)
			provider := &nativeHistoryConversation{fakeConversation: newFakeConversation(), events: historicalNativeHistory()}
			provider.providerConversationID = historicalTargetThread
			if scenario == "wrong_provider" {
				provider.providerConversationID = "unrelated-thread"
			}
			if scenario == "history_failure" {
				provider.err = errors.New("transcript unavailable")
			}
			providerCalls := 0
			driver := fakeDriver{start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
				return nil, errors.New("recovery must not start a fresh provider")
			}, resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
				providerCalls++
				if cfg.ProviderScopeID != handoff.BoundaryID {
					t.Fatalf("unreserved provider namespace: %s", cfg.ProviderScopeID)
				}
				current, err := fixture.store.ProjectConversation(ctx, testProject)
				if err != nil || current.SessionID != fixture.target.ID || current.ActiveBranchID != fixture.root.ID {
					t.Fatalf("ownership changed before provider I/O: %+v err=%v", current, err)
				}
				switch scenario {
				case "provider_failure":
					return nil, errors.New("provider unavailable")
				case "predecessor_revived":
					rec := fixture.source
					rec.IsTerminated = false
					if err := fixture.store.UpdateSession(ctx, rec); err != nil {
						t.Fatal(err)
					}
				case "competing_orchestrator":
					if _, err := fixture.store.CreateSession(ctx, domain.SessionRecord{
						ProjectID: testProject, Kind: domain.KindOrchestrator,
						Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
						CreatedAt: fixture.now.Add(time.Hour), UpdatedAt: fixture.now.Add(time.Hour),
					}); err != nil {
						t.Fatal(err)
					}
				case "head_moved":
					mover, err := fixture.store.CreateSession(ctx, domain.SessionRecord{
						ProjectID: testProject, Kind: domain.KindWorker,
						Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
						Metadata:  domain.SessionMetadata{ProviderConversationID: "worker-thread"},
						CreatedAt: fixture.now.Add(time.Hour), UpdatedAt: fixture.now.Add(time.Hour),
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := fixture.store.CreateAndActivateConversationBranch(ctx, mover.ID, domain.ConversationBranch{
						ID: "competing-epoch", ConversationID: fixture.conversation.ID, SessionID: mover.ID,
						ProviderConversationID: "worker-thread", ParentBranchID: fixture.root.ID,
						ForkAfterSequence: fixture.conversation.LatestSequence, ProviderScopeID: "competing-epoch",
						ProviderIDsScoped: true, CreatedAt: fixture.now.Add(time.Hour),
					}, "worker-generation", fixture.now.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				case "history_changed":
					if err := fixture.store.UpsertActivity(ctx, fixture.conversation.ID, "", domain.ConversationActivity{
						ID: "concurrent-history", ProviderItemID: "concurrent-history",
						Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
					}, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				return provider, nil
			}}
			lcm := lifecycle.New(fixture.store, nil)
			svc := chatsvc.New(chatsvc.Options{
				Store: fixture.store, Sessions: fixture.store, Reader: snapshotReader(fixture.store),
				Drivers: fakeRegistry{driver: driver}, NewID: uuid.NewString,
			})
			t.Cleanup(func() { svc.StopAll(ctx) })
			cfg := chatsvc.StartConfig{
				SessionID: fixture.target.ID, ProjectID: testProject,
				Kind: domain.KindOrchestrator, Harness: fixture.target.Harness,
				WorkspacePath:          fixture.target.Metadata.WorkspacePath,
				ProviderConversationID: historicalTargetThread, ProviderHandoff: handoff,
				ExpectedControllerOwner: fixture.target.ControllerOwner(),
				ControllerReady: func(started chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
					metadata := fixture.target.Metadata
					metadata.ControllerGeneration = started.ControllerGeneration
					err := lcm.MarkChatSpawnedPrepared(ctx, fixture.target.ID, metadata,
						*started.ProviderBoundary, handoff, started.CommitProviderHistory)
					committed := started.Conversation
					committed.ActiveBranchID = handoff.BoundaryID
					committed.SessionID = fixture.target.ID
					return chatsvc.ControllerCommit{Conversation: committed}, err
				},
			}
			_, err := svc.Start(ctx, cfg)
			switch scenario {
			case "success":
				if err != nil {
					t.Fatalf("recovery publication: %v", err)
				}
			case "provider_failure":
				if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
					t.Fatalf("provider failure error = %v", err)
				}
			case "wrong_provider":
				if err == nil || !strings.Contains(err.Error(), "does not match requested handle") {
					t.Fatalf("wrong handle error = %v", err)
				}
			case "history_failure":
				if err == nil || !strings.Contains(err.Error(), "transcript unavailable") {
					t.Fatalf("history failure error = %v", err)
				}
			case "predecessor_revived":
				if err == nil || !strings.Contains(err.Error(), "not a retired predecessor") {
					t.Fatalf("revived predecessor error = %v", err)
				}
			case "competing_orchestrator":
				if err == nil || !strings.Contains(err.Error(), "competing live orchestrator") {
					t.Fatalf("competing orchestrator error = %v", err)
				}
			case "head_moved":
				if err == nil || !strings.Contains(err.Error(), "active branch changed") {
					t.Fatalf("moved head error = %v", err)
				}
			case "history_changed":
				if err == nil || !strings.Contains(err.Error(), "handoff history changed") {
					t.Fatalf("history growth error = %v", err)
				}
			}
			if scenario == "success" {
				if providerCalls != 1 {
					t.Fatalf("provider calls = %d, want 1", providerCalls)
				}
				conversation, err := fixture.store.ConversationForSession(ctx, fixture.target.ID)
				if err != nil || conversation.ActiveBranchID != "recovery-epoch" {
					t.Fatalf("recovery epoch not active: %+v err=%v", conversation, err)
				}
				stale, err := fixture.store.ConversationBranch(ctx, conversation.ID, fixture.root.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stale.Active || stale.SessionID != fixture.source.ID ||
					stale.ProviderConversationID != "native-428" ||
					stale.ParentBranchID != fixture.root.ParentBranchID ||
					stale.ProviderScopeID != fixture.root.ProviderScopeID ||
					stale.ProviderIDsScoped != fixture.root.ProviderIDsScoped {
					t.Fatalf("stale epoch was rewritten: %+v", stale)
				}
				root, err := fixture.store.ConversationBranch(ctx, conversation.ID, stale.ParentBranchID)
				if err != nil || root.SessionID != fixture.source.ID || root.ProviderConversationID != "native-88" {
					t.Fatalf("conversation root was rewritten: %+v err=%v", root, err)
				}
				boundary, err := fixture.store.ConversationBranch(ctx, conversation.ID, "recovery-epoch")
				if err != nil {
					t.Fatal(err)
				}
				if boundary.ParentBranchID != fixture.root.ID || boundary.SessionID != fixture.target.ID ||
					boundary.ProviderConversationID != historicalTargetThread ||
					boundary.ProviderScopeID != "recovery-epoch" {
					t.Fatalf("recovery epoch = %+v", boundary)
				}
				snapshot, err := fixture.store.LoadConversationSnapshot(ctx, conversation.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Messages) != 3 ||
					snapshot.Messages[0].Text != "preserve the old orchestrator transcript" ||
					snapshot.Messages[1].Text != "restore my prior question" ||
					snapshot.Messages[2].Text != "restored prior answer" {
					t.Fatalf("published transcript = %+v", snapshot.Messages)
				}
				events, err := fixture.store.ProviderEventsSince(ctx, conversation.ID, 0, 20)
				if err != nil || len(events) != 5 {
					t.Fatalf("published provider events = %+v err=%v, want five (context boundary + native turn)", events, err)
				}
				for _, event := range events {
					if event.BranchID != "recovery-epoch" || event.SessionID != fixture.target.ID {
						t.Fatalf("provider event left the recovery namespace: %+v", event)
					}
				}
				return
			}
			if scenario != "wrong_provider" && providerCalls != 1 {
				t.Fatalf("provider calls = %d, want 1", providerCalls)
			}
			if scenario == "wrong_provider" && providerCalls != 1 {
				t.Fatalf("provider calls = %d, want 1", providerCalls)
			}
			if scenario == "head_moved" {
				// The competing worker legitimately moved the head, so only the
				// recovery's own non-publication is asserted here.
				if _, err := fixture.store.ConversationBranch(ctx, fixture.conversation.ID, "recovery-epoch"); !errors.Is(err, domain.ErrNoConversationBranch) {
					t.Fatalf("moved head left a recovery boundary: %v", err)
				}
				rec, _, err := fixture.store.GetSession(ctx, fixture.target.ID)
				if err != nil || rec.Metadata.ControllerGeneration != "" {
					t.Fatalf("moved head published the replacement: %+v err=%v", rec, err)
				}
				return
			}
			assertReboundFixtureUnchanged(t, fixture)
		})
	}
}

// A committed recovery epoch is consumed: the next ordinary resume finds the
// replacement's own identity on the active branch and must not stack a second
// epoch on top of it.
func TestReboundOrchestratorRecoveryRepeatResumeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := seedReboundOrchestratorFixture(t)
	handoff := reboundRecoveryHandoff(fixture)
	conversations := []*nativeHistoryConversation{
		{fakeConversation: newFakeConversation(), events: historicalNativeHistory()},
		{fakeConversation: newFakeConversation(), events: historicalNativeHistory()},
	}
	for _, conversation := range conversations {
		conversation.providerConversationID = historicalTargetThread
	}
	var scopes []string
	driver := fakeDriver{resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
		scopes = append(scopes, cfg.ProviderScopeID)
		if len(conversations) == 0 {
			return nil, errors.New("no provider conversation queued")
		}
		conversation := conversations[0]
		conversations = conversations[1:]
		return conversation, nil
	}}
	lcm := lifecycle.New(fixture.store, nil)
	svc := chatsvc.New(chatsvc.Options{
		Store: fixture.store, Sessions: fixture.store, Reader: snapshotReader(fixture.store),
		Drivers: fakeRegistry{driver: driver}, NewID: uuid.NewString,
	})
	t.Cleanup(func() { svc.StopAll(ctx) })
	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: fixture.target.ID, ProjectID: testProject,
		Kind: domain.KindOrchestrator, Harness: fixture.target.Harness,
		WorkspacePath:          fixture.target.Metadata.WorkspacePath,
		ProviderConversationID: historicalTargetThread, ProviderHandoff: handoff,
		ExpectedControllerOwner: fixture.target.ControllerOwner(),
		ControllerReady: func(started chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			metadata := fixture.target.Metadata
			metadata.ControllerGeneration = started.ControllerGeneration
			if err := lcm.MarkChatSpawnedPrepared(ctx, fixture.target.ID, metadata,
				*started.ProviderBoundary, handoff, started.CommitProviderHistory); err != nil {
				return chatsvc.ControllerCommit{}, err
			}
			committed := started.Conversation
			committed.ActiveBranchID = handoff.BoundaryID
			committed.SessionID = fixture.target.ID
			return chatsvc.ControllerCommit{Conversation: committed}, nil
		},
	})
	if err != nil {
		t.Fatalf("first recovery: %v", err)
	}
	if err := svc.Stop(ctx, fixture.target.ID); err != nil {
		t.Fatalf("stop recovered controller: %v", err)
	}
	// The ordinary retry carries no reservation: the active branch now holds the
	// replacement's own handle, so a plain native resume applies.
	restored, found, err := fixture.store.GetSession(ctx, fixture.target.ID)
	if err != nil || !found {
		t.Fatalf("load restored replacement: found=%v err=%v", found, err)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: fixture.target.ID, ProjectID: testProject,
		Kind: domain.KindOrchestrator, Harness: fixture.target.Harness,
		WorkspacePath:          fixture.target.Metadata.WorkspacePath,
		ProviderConversationID: historicalTargetThread,
		ControllerReady:        historicalControllerReady(lcm, restored),
	}); err != nil {
		t.Fatalf("ordinary retry after committed epoch: %v", err)
	}
	branches, err := fixture.store.ConversationBranches(ctx, fixture.conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 3 || len(scopes) != 2 || scopes[1] != "recovery-epoch" {
		t.Fatalf("retry stacked a duplicate epoch: branches=%+v scopes=%v", branches, scopes)
	}
}

// A same-session handle mismatch is an arbitrary true conflict: the session
// claims a thread the active branch — which it owns — does not hold. Recovery
// must never adopt it, and the ordinary resume must keep rejecting it before
// any provider I/O.
func TestOrdinaryResumeRejectsSameSessionHandleMismatch(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)
	source, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	source.Metadata.ProviderConversationID = "native-X"
	source.Metadata.ControllerGeneration = "generation-X"
	source.Mode = domain.SessionModeChat
	source.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	source.UpdatedAt = now
	if err := st.UpdateSession(ctx, source); err != nil {
		t.Fatal(err)
	}
	conversation, err := st.CreateConversation(ctx, "same-session-conversation",
		domain.ConversationScopeProject, testProject, source.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAndActivateConversationBranch(ctx, source.ID, domain.ConversationBranch{
		ID: "owned-epoch", ConversationID: conversation.ID, SessionID: source.ID,
		ProviderConversationID: "native-X", ParentBranchID: conversation.ActiveBranchID,
		ForkAfterSequence: conversation.LatestSequence, ProviderScopeID: "owned-epoch",
		ProviderIDsScoped: true, CreatedAt: now.Add(time.Second),
	}, "generation-X", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// The session record now claims a different real handle than the branch it
	// owns: exactly the arbitrary mismatch recovery must leave rejected.
	source.Metadata.ProviderConversationID = "native-Y"
	if err := st.UpdateSession(ctx, source); err != nil {
		t.Fatal(err)
	}
	resumeCalls := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Reader: snapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{resume: func(ports.ChatResumeConfig) (ports.ChatConversation, error) {
			resumeCalls++
			return nil, errors.New("must not resume an unproven thread")
		}}},
		NewID: func() string { return "unused-generation" },
	})
	t.Cleanup(func() { svc.StopAll(ctx) })
	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: source.ID, ProjectID: testProject,
		Kind: domain.KindOrchestrator, Harness: source.Harness,
		ProviderConversationID: "native-Y",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match session handle") {
		t.Fatalf("same-session mismatch error = %v", err)
	}
	if resumeCalls != 0 {
		t.Fatalf("provider resume calls = %d, want rejection before provider I/O", resumeCalls)
	}
	current, err := st.ConversationForSession(ctx, source.ID)
	if err != nil || current.ActiveBranchID != "owned-epoch" {
		t.Fatalf("rejected mismatch moved the head: %+v err=%v", current, err)
	}
}
