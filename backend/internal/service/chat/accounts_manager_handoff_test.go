package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestAccountsManagerChatHandoffPausesQueueWithoutCancellingIt(t *testing.T) {
	for _, policy := range []domain.SessionInterfaceTransitionPolicy{domain.SessionInterfaceTransitionDrain, domain.SessionInterfaceTransitionInterrupt} {
		t.Run(string(policy), func(t *testing.T) {
			h := newHarness(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "running", ClientMessageID: "account-1"}); err != nil {
				t.Fatal(err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
			})
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", ClientMessageID: "account-2"}); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.ArmAccountsManagerHandoff(ctx, testSession, true); !errors.Is(err, chatsvc.ErrTurnRunning) {
				t.Fatal("fresh conversation could discard the queue", err)
			}
			if err := h.svc.ArmAccountsManagerHandoff(ctx, testSession, false); err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "late", ClientMessageID: "account-3"}); !errors.Is(err, chatsvc.ErrControllerHandoff) {
				t.Fatal("late input was accepted")
			}
			done := make(chan error, 1)
			go func() { done <- h.svc.PrepareAccountsManagerHandoff(ctx, testSession, policy) }()
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("handoff did not finish")
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return turnStateByText(t, s)["running"].Terminal() })
			if h.conv.sendCallCount() != 1 {
				t.Fatal("queue used the old account")
			}
			if err := h.svc.AcknowledgeAccountsManagerSwitch(ctx, testSession, "wrong-generation", func(context.Context) error { t.Error("wrong generation was acknowledged"); return nil }); err == nil {
				t.Fatal("wrong generation accepted")
			}
			failure := errors.New("durable acknowledgement unavailable")
			if err := h.svc.AcknowledgeAccountsManagerSwitch(ctx, testSession, h.ctrl.Generation(), func(context.Context) error { return failure }); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if h.conv.sendCallCount() != 1 {
				t.Fatal("failed acknowledgement reopened queue")
			}
			h.svc.AbortAccountsManagerHandoff(testSession)
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["queued"] == domain.TurnStateRunning
			})
			if h.conv.sendCallCount() != 2 {
				t.Fatal("cancelled switch did not resume queue exactly once")
			}
		})
	}
}

func TestAccountsManagerChatQueueSurvivesSwitchFailureUntilExplicitCancellation(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	for _, msg := range []ports.ChatUserMessage{{Text: "running", ClientMessageID: "q1"}, {Text: "queued", ClientMessageID: "q2"}} {
		if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
			t.Fatal(err)
		}
	}
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := h.st.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerNative})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := h.st.CreateAccountsManagerSwitch(ctx, domain.AccountsManagerSwitch{ID: "queue-switch", SessionID: rec.ID, Provider: binding.Provider,
		SourceMode: binding.Mode, SourceRevision: binding.Revision, SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID,
		TargetMode: domain.AccountsManagerManaged, TargetAccountID: "account-b", TargetGeneration: "target", Policy: domain.SessionInterfaceTransitionInterrupt})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ArmAccountsManagerHandoff(ctx, testSession, false); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return turnStateByText(t, s)["running"].Terminal() })
	if turnStateByText(t, snapshot)["queued"] != domain.TurnStateQueued {
		t.Fatal("controller stop discarded paused queue")
	}
	if _, err := h.st.AdvanceAccountsManagerSwitch(ctx, op.ID, op.Phase, domain.AccountsManagerSwitchFailed, "TEST_SETTLED"); err != nil {
		t.Fatal(err)
	}
	if err := h.st.SettleOrphanedTurns(ctx, testSession, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err = h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if turnStateByText(t, snapshot)["queued"] != domain.TurnStateQueued {
		t.Fatal("switch failure discarded accepted queue")
	}
	for _, turn := range snapshot.Turns {
		if turn.State == domain.TurnStateQueued {
			if err := h.st.CancelQueuedTurnByID(ctx, h.ctrl.ConversationID(), turn.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				for _, current := range s.Turns {
					if current.ID == turn.ID {
						return current.State == domain.TurnStateCancelled
					}
				}
				return false
			})
		}
	}
}

type accountSwitchPendingReader struct{ fakeChatAccountsManager }

func (accountSwitchPendingReader) AgentAccountSwitchPending(context.Context, domain.SessionID) (bool, error) {
	return true, nil
}

func TestAccountsManagerChatTargetIsFencedBeforePublication(t *testing.T) {
	st := openStore(t)
	conv := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{Store: st, Sessions: st, Drivers: fakeRegistry{driver: fakeDriver{conv: conv}}, AccountsManager: accountSwitchPendingReader{}, NewID: uuid.NewString, Now: time.Now, Log: slog.New(slog.DiscardHandler)})
	ctrl, err := svc.Start(t.Context(), chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	if _, err := svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "too early", ClientMessageID: "target-1"}); !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatal("target accepted input before acknowledgement", err)
	}
	committed := false
	if err := svc.AcknowledgeAccountsManagerSwitch(t.Context(), testSession, ctrl.Generation(), func(context.Context) error { committed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(t.Context(), testSession, ports.ChatUserMessage{Text: "ready", ClientMessageID: "target-2"}); err != nil || !committed {
		t.Fatal("acknowledged target rejected input", err)
	}
}

func TestAccountsManagerChatStartupPreservesUndispatchedQueue(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "pending"
		if cancelled {
			name = "cancelled before queue adoption"
		}
		t.Run(name, func(t *testing.T) { testAccountQueueColdRestart(t, cancelled, false, false) })
	}
}

func TestAccountsManagerChatCancellationAfterStartupAdoptsQueue(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		name := "requested"
		if waiting {
			name = "waiting"
		}
		t.Run(name, func(t *testing.T) { testAccountQueueColdRestart(t, true, true, waiting) })
	}
}

func testAccountQueueColdRestart(t *testing.T, cancelled, cancelAfterRestart, waiting bool) {
	t.Helper()
	h := newHarness(t)
	ctx := t.Context()
	for _, msg := range []ports.ChatUserMessage{{Text: "running", ClientMessageID: "restart-1"}, {Text: "queued", ClientMessageID: "restart-2"}} {
		if _, err := h.svc.Send(ctx, testSession, msg); err != nil {
			t.Fatal(err)
		}
	}
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := h.st.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: rec.ID, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerNative})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = h.st.CreateAccountsManagerSwitch(ctx, domain.AccountsManagerSwitch{ID: "restart-switch", SessionID: rec.ID, Provider: binding.Provider, SourceMode: binding.Mode,
		SourceRevision: binding.Revision, SourceOwner: rec.ControllerOwner(), SourceRuntimeHandleID: rec.Metadata.RuntimeHandleID,
		TargetMode: domain.AccountsManagerManaged, TargetAccountID: "target", TargetGeneration: "replacement", Policy: domain.SessionInterfaceTransitionDrain})
	if err != nil {
		t.Fatal(err)
	}
	if waiting {
		if _, err := h.st.AdvanceAccountsManagerSwitch(ctx, "restart-switch", domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.svc.ArmAccountsManagerHandoff(ctx, testSession, false); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	restarted := sessionmanager.New(sessionmanager.Deps{Store: h.st, BackgroundContext: ctx})
	if cancelled && !cancelAfterRestart {
		if _, err := restarted.CancelAccountsManagerSwitch(ctx, testSession, "restart-switch"); err != nil {
			t.Fatal(err)
		}
	}
	project, found, err := h.st.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !found {
		t.Fatalf("restart database directory: found=%v err=%v", found, err)
	}
	if err := h.st.Close(); err != nil {
		t.Fatal(err)
	}
	h.st, err = sqlite.OpenPreMigrated(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.st.Close() })
	restarted = sessionmanager.New(sessionmanager.Deps{Store: h.st, BackgroundContext: ctx})
	if err := restarted.ReconcileStartupSafety(ctx); err != nil {
		t.Fatal(err)
	}
	if cancelAfterRestart {
		if release, admitted := restarted.AcquireSessionInput(testSession); admitted {
			release()
			t.Fatal("restart did not restore the fence")
		}
		if _, err := restarted.CancelAccountsManagerSwitch(ctx, testSession, "restart-switch"); err != nil {
			t.Fatalf("pre-stop cancellation after restart: %v", err)
		}
		if release, admitted := restarted.AcquireSessionInput(testSession); !admitted {
			t.Fatal("cancellation did not release the restored fence")
		} else {
			release()
		}
	}
	if err := h.st.SettleOrphanedTurns(ctx, testSession, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if state := turnStateByText(t, snapshot)["queued"]; state != domain.TurnStateQueued {
		t.Fatalf("startup discarded undispatched work: state=%s", state)
	}
	if h.conv.sendCallCount() != 1 {
		t.Fatal("startup replayed queued work")
	}
	if cancelled {
		replacement := newFakeConversation()
		replacement.turnSeq = 1
		svc := chatsvc.New(chatsvc.Options{Store: h.st, Sessions: h.st, Drivers: fakeRegistry{driver: fakeDriver{conv: replacement}}, NewID: uuid.NewString, Now: time.Now, Log: slog.New(slog.DiscardHandler)})
		if _, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: rec.Harness, WorkspacePath: t.TempDir(), ProviderConversationID: h.conv.ProviderConversationID(), HistoryMode: ports.ChatHistoryDeferred}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
		for range 2 {
			if err := svc.DrainQueued(ctx, testSession); err != nil {
				t.Fatal(err)
			}
		}
		if replacement.sendCallCount() != 1 {
			t.Fatal("cold controller did not adopt the queue exactly once")
		}
		replacement.mu.Lock()
		defer replacement.mu.Unlock()
		if len(replacement.sent) != 1 || replacement.sent[0].Text != "queued" {
			t.Fatal("cold controller replayed previously dispatched work")
		}
	}
}
