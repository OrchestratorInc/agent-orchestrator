package chat

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
