package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sideCleanupTestConversation struct {
	deferredSideTestConversation
	deleted []string
}

func (c *sideCleanupTestConversation) DeleteFork(_ context.Context, id string) error {
	c.deleted = append(c.deleted, id)
	return nil
}

func TestSideCleanupPreservesOpenForkUntilClosed(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "created", true: "recovered"}[recovered], func(t *testing.T) {
			ctx := context.Background()
			svc := New(Options{AppRunID: "run", Sessions: sideSessionReader{}})
			store := svc.sides.store
			now := time.Now().UTC()
			if _, err := store.ClaimSideLaunch(ctx, "run", now); err != nil {
				t.Fatal(err)
			}
			side := domain.SideConversation{PolicyVersion: 1, ID: "side", SessionID: "session", MainConversationID: "main",
				AppRunID: "run", ProviderHostID: "btw-run-side", Generation: "g", State: "opening"}
			if _, _, err := store.CreateSideConversation(ctx, side); err != nil {
				t.Fatal(err)
			}
			if err := store.RegisterSideFork(ctx, side.ID, side.Generation, "fork", now); err != nil {
				t.Fatal(err)
			}
			if err := store.SetSideReady(ctx, side.ID, side.Generation, "fork", now); err != nil {
				t.Fatal(err)
			}
			if recovered {
				records, err := svc.ExportSideChatLaunch(ctx, "run")
				if err != nil {
					t.Fatal(err)
				}
				fresh := newMemorySideStore()
				if _, err := fresh.ClaimSideLaunch(ctx, "run", now); err != nil {
					t.Fatal(err)
				}
				if _, err := fresh.recover("run", records, now); err != nil {
					t.Fatal(err)
				}
				svc.sides.store = fresh
			}
			conv := &sideCleanupTestConversation{}
			svc.controllers[side.SessionID] = &Controller{conv: conv}
			svc.sides.retryCleanup()
			if len(conv.deleted) != 0 {
				t.Fatalf("cleanup deleted an open fork: %v", conv.deleted)
			}
			if err := svc.CloseIndependentSideChat(ctx, side.SessionID, side.ID); err != nil {
				t.Fatal(err)
			}
			svc.sides.retryCleanup()
			if len(conv.deleted) != 1 || conv.deleted[0] != "fork" {
				t.Fatalf("closed fork deletions = %v, want [fork]", conv.deleted)
			}
		})
	}
}

func TestSideClosedCleanupSurvivesRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	svc := New(Options{AppRunID: "run", Sessions: sideSessionReader{}})
	store := svc.sides.store
	_, _ = store.ClaimSideLaunch(ctx, "run", now)
	side := domain.SideConversation{PolicyVersion: 1, ID: "side", SessionID: "session", MainConversationID: "main", AppRunID: "run", ProviderHostID: "btw-run-side", Generation: "g", State: "opening"}
	_, _, _ = store.CreateSideConversation(ctx, side)
	_ = store.RegisterSideFork(ctx, "side", "g", "fork", now)
	_ = store.SetSideReady(ctx, "side", "g", "fork", now)
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { return errors.New("cancelled") }
	if err := svc.CloseIndependentSideChat(ctx, "session", "side"); err == nil {
		t.Fatal("expected stop failure")
	}
	records, err := svc.ExportSideChatLaunch(ctx, "run")
	if err != nil || len(records) != 1 || !records[0].CleanupOnly {
		t.Fatalf("cleanup export: %+v %v", records, err)
	}
	fresh := newMemorySideStore()
	_, _ = fresh.ClaimSideLaunch(ctx, "run", now)
	if sides, err := fresh.recover("run", records, now); err != nil || len(sides) != 0 {
		t.Fatalf("reopened closed tab: %+v %v", sides, err)
	}
	svc.sides.store = fresh
	conv := &sideCleanupTestConversation{}
	svc.controllers["session"] = &Controller{conv: conv}
	svc.sides.retryCleanup()
	if len(conv.deleted) != 0 || len(fresh.SideHostCleanupPending(ctx)) != 1 {
		t.Fatal("deleted fork before host stopped")
	}
	svc.stopProviderHost = func(context.Context, domain.SessionID) error { return nil }
	svc.sides.retryCleanup()
	if len(conv.deleted) != 1 || len(fresh.export("run")) != 0 {
		t.Fatal("cleanup did not finish")
	}
}

type restoredHistoryConversation struct {
	deferredSideTestConversation
	history []ports.ChatEvent
}

func (c *restoredHistoryConversation) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return c.history, nil
}
func TestSideRestoredRunningTurnReconcilesTerminalHistory(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "completed"}[verified], func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			svc := New(Options{AppRunID: "run"})
			store := svc.sides.store
			_, _ = store.ClaimSideLaunch(ctx, "run", now)
			side := domain.SideConversation{ID: "side", SessionID: "session", MainConversationID: "main", AppRunID: "run", ProviderHostID: "btw-run-side", Harness: domain.HarnessCodex, Generation: "g", State: "ready"}
			_, _, _ = store.CreateSideConversation(ctx, side)
			_, _, _ = store.ReserveSideTurn(ctx, domain.SideTurn{ID: "turn", SideID: "side", ClientMessageID: "client", State: "running", ProviderTurnID: "native", CreatedAt: now}, "run")
			_, _, _, _ = store.ClaimNextSideTurn(ctx, "run", nil, now)
			_ = store.SetSideTurnProviderID(ctx, "side", "turn", "g", "native")
			conv := &restoredHistoryConversation{}
			if verified {
				conv.history = []ports.ChatEvent{{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "native", TurnState: domain.TurnStateCompleted}}
			}
			err := svc.sides.reconcileRestoredTurns(ctx, side, conv)
			turns, _, _ := store.SideTurns(ctx, "side", time.Time{}, 0)
			if verified {
				if err != nil || turns[0].State != "completed" {
					t.Fatalf("not reconciled: %+v %v", turns, err)
				}
			} else {
				if err == nil || turns[0].State != "running" {
					t.Fatal("unknown outcome was silently replayed")
				}
			}
		})
	}
}
