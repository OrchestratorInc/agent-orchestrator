package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestLaunchRefreshDeliversNextMessageOnceToResumedConversation(t *testing.T) {
	h, old, resumed := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	ctx := context.Background()
	if err := h.svc.SetChatView(ctx, testSession, "open-view", true); err != nil {
		t.Fatal(err)
	}
	// Native resume must retain the same provider conversation, not start fresh.
	wakeOnNewRelease(t, h, old, nil)
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "use the task tools", ClientMessageID: "launch-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := resumed.sentTexts(); len(got) == 1 {
			if got[0] != "use the task tools" || len(old.sentTexts()) != 1 || old.calls.Load() != 1 {
				t.Fatalf("delivery: old=%v new=%v stops=%d", old.sentTexts(), got, old.calls.Load())
			}
			_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "use the task tools", ClientMessageID: "launch-refresh"})
			if err != nil || len(resumed.sentTexts()) != 1 {
				t.Fatalf("duplicate delivery: err=%v sent=%v", err, resumed.sentTexts())
			}
			snapshot, err := h.svc.Snapshot(ctx, testSession)
			if err != nil || len(snapshot.Turns) != 2 || snapshot.Turns[1].ID != turn.ID {
				t.Fatalf("durable turn identity changed: turns=%+v err=%v", snapshot.Turns, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resumed provider did not receive queued work: %v", resumed.sentTexts())
}

func TestLaunchRefreshSweepRunsWithHibernationDisabled(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	woke := wakeOnNewRelease(t, h, old, nil)
	if refreshed, err := h.svc.HibernateChat(context.Background(), testSession); err != nil || !refreshed {
		t.Fatalf("refresh=%v err=%v", refreshed, err)
	}
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("idle refresh did not resume the native conversation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.svc.WaitForWake(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := h.svc.HibernateChat(ctx, testSession); err != nil || refreshed || old.calls.Load() != 1 {
		t.Fatalf("current provider refreshed again: refreshed=%v err=%v stops=%d", refreshed, err, old.calls.Load())
	}
}

func TestLaunchRefreshFailedResumeKeepsAcceptedMessageQueued(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	wakeOnNewRelease(t, h, old, errors.New("resume temporarily unavailable"))
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "keep this work", ClientMessageID: "refresh-failure"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.svc.WaitForWake(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	snapshot, err := h.svc.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range snapshot.Turns {
		if saved.ID == turn.ID {
			if saved.State != domain.TurnStateQueued {
				t.Fatalf("accepted work became %s", saved.State)
			}
			return
		}
	}
	t.Fatal("accepted message was lost after failed resume")
}

func TestLaunchRefreshKeepsWorkAndUnknownBackgroundStateAlive(t *testing.T) {
	for _, scenario := range []string{"running", "queued", "approval", "input", "background", "background error", "viewed"} {
		t.Run(scenario, func(t *testing.T) {
			h, old, _ := versionRefreshHarness(t)
			old.launchRefresh.Store(true)
			ctx := context.Background()
			switch scenario {
			case "running":
				if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession, h.ctrl.Generation(), "running", "provider-running", h.now()); err != nil {
					t.Fatal(err)
				}
			case "queued":
				_, err := h.st.AppendUserMessage(ctx, h.ctrl.ConversationID(), testSession, h.ctrl.Generation(), domain.ConversationMessage{ID: "queued", Text: "queued", Origin: domain.MessageOriginDaemon}, "queued", h.now())
				if err != nil {
					t.Fatal(err)
				}
			case "approval", "input":
				event := ports.ChatEvent{ProviderTurnID: "provider-turn-1", ProviderItemID: "decision", RequestID: "decision", Summary: "Need a decision"}
				if scenario == "approval" {
					event.Kind = ports.ChatEventApprovalRequested
					event.Decisions = []ports.ChatDecisionOption{{ID: "allow", Label: "Allow", Kind: ports.ChatDecisionAllowOnce}}
				} else {
					event.Kind = ports.ChatEventInputRequested
					event.Input = &ports.ChatInputRequest{Message: "Choose"}
				}
				old.emit(event)
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					return len(s.Activities) > 0 && s.Activities[len(s.Activities)-1].Status == domain.ActivityStatusPending
				})
			case "background":
				old.backgroundRunning = true
			case "background error":
				old.backgroundErr = errors.New("unknown background state")
			case "viewed":
				if err := h.svc.SetChatView(ctx, testSession, "view", true); err != nil {
					t.Fatal(err)
				}
			}
			refreshed, err := h.svc.HibernateChat(ctx, testSession)
			if refreshed || old.calls.Load() != 0 || !h.svc.HasLiveChatController(testSession) {
				t.Fatalf("unsafe refresh: refreshed=%v err=%v stops=%d", refreshed, err, old.calls.Load())
			}
		})
	}
}

func TestLaunchRefreshOrchestratorIsIncludedInSweep(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	ctx := context.Background()
	rec, _, _ := h.st.GetSession(ctx, testSession)
	rec.Kind = domain.KindOrchestrator
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	ids, err := h.st.ListChatHibernationCandidates(ctx)
	if err != nil || len(ids) != 1 || ids[0] != testSession {
		t.Fatalf("candidates=%v err=%v", ids, err)
	}
	old.launchRefresh.Store(true)
	wakeOnNewRelease(t, h, old, nil)
	if refreshed, err := h.svc.HibernateChat(ctx, testSession); err != nil || !refreshed {
		t.Fatalf("refresh=%v err=%v", refreshed, err)
	}
	waitForLaunchRefresh(t, h)
}

func waitForLaunchRefresh(t *testing.T, h *harness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.svc.WaitForWake(ctx, testSession); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRefreshPartialShutdownFailureKeepsQueue(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	old.shutdownErr = errors.New("shutdown lost")
	old.hostShutdownErr = errors.New("host unavailable")
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "keep through partial shutdown", ClientMessageID: "partial-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	waitForLaunchRefresh(t, h)
	pending, err := h.st.SessionLaunchRefreshPending(context.Background(), testSession)
	if err != nil || !pending {
		t.Fatalf("refresh intent=%v err=%v", pending, err)
	}
	snapshot, err := h.svc.Snapshot(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, saved := range snapshot.Turns {
		if saved.ID == turn.ID && saved.State == domain.TurnStateQueued {
			return
		}
	}
	t.Fatalf("accepted work not queued: %+v", snapshot.Turns)
}

func TestLaunchRefreshFailedResumeSurvivesServiceRestart(t *testing.T) {
	h, old, resumed := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	ctx := context.Background()
	// Match Session Manager: clear the sleep marker before attempting resume.
	h.svc.SetWakeCallback(func(ctx context.Context, id domain.SessionID) error {
		if _, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); err != nil {
			t.Errorf("wake began before the triggering message was durable: %v", err)
			return err
		}
		// Queue projection can advance the revision while wake clears the marker.
		// Match the manager's retry rather than treating a rejected CAS as success.
		for range 3 {
			rec, _, err := h.st.GetSession(ctx, id)
			if err != nil {
				return err
			}
			cleared, err := h.st.SetSessionHibernated(ctx, id, rec.Revision, nil)
			if err != nil {
				return err
			}
			if cleared {
				return errors.New("replacement unavailable")
			}
		}
		return errors.New("sleep marker changed concurrently")
	})
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "survive daemon restart", ClientMessageID: "restart-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	waitForLaunchRefresh(t, h)
	pending, err := h.st.SessionLaunchRefreshPending(ctx, testSession)
	if err != nil || !pending {
		t.Fatalf("refresh intent=%v err=%v", pending, err)
	}
	rec, _, _ := h.st.GetSession(ctx, testSession)
	if rec.HibernatedAt != nil {
		t.Fatal("failed resume should expose Resume")
	}
	// A fresh Service has none of the old wake/update maps.
	var nextID atomic.Int32
	replacement := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{resume: func(cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
			if cfg.ProviderConversationID != old.ProviderConversationID() {
				t.Errorf("native history identity changed: %+v", cfg)
			}
			return resumed, nil
		}}},
		Activity: h.activity, Log: slog.New(slog.DiscardHandler), Now: h.now,
		NewID: func() string { return fmt.Sprintf("replacement-%d", nextID.Add(1)) },
	})
	t.Cleanup(func() { _ = replacement.Stop(ctx, testSession) })
	if _, err := replacement.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: old.ProviderConversationID()}); err != nil {
		t.Fatal(err)
	}
	pending, err = h.st.SessionLaunchRefreshPending(ctx, testSession)
	if err != nil || pending {
		t.Fatalf("successful admission left intent=%v err=%v", pending, err)
	}
	snapshot, err := replacement.Snapshot(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, saved := range snapshot.Turns {
		if saved.ID == turn.ID {
			found = true
			if saved.State != domain.TurnStateQueued {
				t.Fatalf("restart settled accepted work: %+v", saved)
			}
		}
	}
	if !found {
		t.Fatal("restart lost accepted work")
	}
	if err := replacement.DrainQueued(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	if got := resumed.sentTexts(); len(got) != 1 || got[0] != "survive daemon restart" {
		t.Fatalf("resumed delivery=%v", got)
	}
}

func TestLaunchRefreshExplicitStopClearsIntentWithoutSleepMarker(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	ctx := context.Background()
	wakeOnNewRelease(t, h, old, errors.New("replacement unavailable"))
	if _, err := h.svc.HibernateChat(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	waitForLaunchRefresh(t, h)
	rec, _, _ := h.st.GetSession(ctx, testSession)
	if _, err := h.st.SetSessionHibernated(ctx, testSession, rec.Revision, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	pending, err := h.st.SessionLaunchRefreshPending(ctx, testSession)
	if err != nil || pending {
		t.Fatalf("explicit stop left intent=%v err=%v", pending, err)
	}
}

func TestLaunchRefreshAdmitsMessageAfterConcurrentWakeClearsSleepMarker(t *testing.T) {
	h, old, _ := versionRefreshHarness(t)
	old.launchRefresh.Store(true)
	ctx := context.Background()
	wakeOnNewRelease(t, h, old, errors.New("replacement unavailable"))
	if stopped, err := h.svc.HibernateChat(ctx, testSession); err != nil || !stopped {
		t.Fatalf("refresh=%v err=%v", stopped, err)
	}
	waitForLaunchRefresh(t, h)
	// Model a concurrent resume clearing the UI marker before failing.
	rec, _, _ := h.st.GetSession(ctx, testSession)
	if cleared, err := h.st.SetSessionHibernated(ctx, testSession, rec.Revision, nil); err != nil || !cleared {
		t.Fatalf("clear marker=%v err=%v", cleared, err)
	}
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "keep concurrent send", ClientMessageID: "concurrent-refresh"})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("send=%+v err=%v", turn, err)
	}
	queued, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || queued.TurnID != turn.ID {
		t.Fatalf("accepted message not retained: %+v err=%v", queued, err)
	}
}
