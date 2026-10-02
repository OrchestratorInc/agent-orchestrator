package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

type removalChatCoordinator interface {
	ArmAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
	StopAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
	AbortAccountsManagerRemoval(context.Context, domain.AccountsManagerRemovalSession) error
}

func TestAccountsManagerRemovalChatColdIdentity(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	identity := strings.Repeat("1a", 32)
	provider := domain.AccountsManagerProviderCodex
	if _, _, err := h.st.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: testSession, Provider: provider, Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.RecordAccountsManagerChatHost(ctx, testSession, provider, h.ctrl.Generation(), identity); err != nil {
		t.Fatal(err)
	}
	if err := h.st.RecordAccountsManagerChatHost(ctx, testSession, provider, h.ctrl.Generation(), strings.Repeat("2b", 32)); !errors.Is(err, domain.ErrAccountsManagerBindingConflict) {
		t.Fatal("controller generation accepted a replacement host identity", err)
	}
	impact, err := h.st.AccountsManagerRemovalImpact(ctx, "account-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.st.CreateAccountsManagerRemoval(ctx, "cold-chat-delete", "account-a", impact.Revision, true); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ArmAccountsManagerRemoval(ctx, impact.Sessions[0]); err != nil {
		t.Fatal(err)
	}
	h.svc.StopAll(ctx)
	project, found, err := h.st.GetProject(ctx, string(testProject))
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := h.st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenPreMigrated(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stops := 0
	cold := chatsvc.New(chatsvc.Options{Store: reopened, Sessions: reopened,
		StopProviderHost: func(context.Context, domain.SessionID) error {
			t.Error("cold deletion used broad teardown")
			return nil
		},
		StopExactProviderHost: func(_ context.Context, id domain.SessionID, got string) error {
			stops++
			if id != testSession || got != identity {
				t.Error("cold teardown lost exact persisted host identity")
			}
			return nil
		},
	})
	for range 2 {
		if err := cold.ArmAccountsManagerRemoval(ctx, impact.Sessions[0]); err != nil {
			t.Fatal(err)
		}
		if err := cold.StopAccountsManagerRemoval(ctx, impact.Sessions[0]); err != nil {
			t.Fatal(err)
		}
	}
	if stops != 2 {
		t.Fatal("cold retry skipped exact stop acknowledgement")
	}
}

func TestAccountsManagerRemovalChatPreservesQueue(t *testing.T) {
	for _, cancelRemoval := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "cancel"}[cancelRemoval], func(t *testing.T) {
			h := newHarness(t)
			for _, msg := range []ports.ChatUserMessage{{Text: "running", ClientMessageID: "remove-1"}, {Text: "queued", ClientMessageID: "remove-2"}} {
				if _, err := h.svc.Send(t.Context(), testSession, msg); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := h.st.GetOrCreateAccountsManagerSessionRoute(t.Context(), domain.AccountsManagerSessionRoute{SessionID: testSession, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "account-a"}); err != nil {
				t.Fatal(err)
			}
			impact, err := h.st.AccountsManagerRemovalImpact(t.Context(), "account-a")
			if err != nil {
				t.Fatal(err)
			}
			op, _, err := h.st.CreateAccountsManagerRemoval(t.Context(), "chat-delete", "account-a", impact.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			entry := op.Impact.Sessions[0]
			if err := h.svc.ArmAccountsManagerRemoval(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			if cancelRemoval {
				if err := h.st.CancelAccountsManagerRemoval(t.Context(), op.ID); err != nil {
					t.Fatal(err)
				}
				if err := h.svc.AbortAccountsManagerRemoval(t.Context(), entry); err != nil {
					t.Fatal(err)
				}
				h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
				h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
					return turnStateByText(t, s)["queued"] == domain.TurnStateRunning
				})
				return
			}
			if err := h.st.BeginAccountsManagerRemovalStop(t.Context(), op.ID); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.StopAccountsManagerRemoval(t.Context(), entry); err != nil {
				t.Fatal(err)
			}
			if err := h.st.SettleOrphanedTurns(t.Context(), testSession, time.Now()); err != nil {
				t.Fatal(err)
			}
			snapshot, err := h.st.LoadConversationSnapshot(t.Context(), h.ctrl.ConversationID())
			if err != nil || turnStateByText(t, snapshot)["queued"] != domain.TurnStateQueued || h.conv.sendCallCount() != 1 {
				t.Fatal("deletion discarded or dispatched accepted queue", err)
			}
		})
	}
}

func TestAccountsManagerRemovalChatExactOwner(t *testing.T) {
	h := newHarness(t)
	coordinator, ok := any(h.svc).(removalChatCoordinator)
	if !ok {
		t.Fatal("managed deletion lacks generation-bound Chat coordination")
	}
	rec, found, err := h.st.GetSession(t.Context(), testSession)
	if err != nil || !found {
		t.Fatal(err)
	}
	entry := domain.AccountsManagerRemovalSession{SessionID: rec.ID, Owner: rec.ControllerOwner()}
	entry.Owner.ControllerGeneration = "retired-generation"
	if err := coordinator.ArmAccountsManagerRemoval(t.Context(), entry); err == nil {
		t.Fatal("foreign controller queue was armed")
	}
	if err := coordinator.StopAccountsManagerRemoval(t.Context(), entry); err == nil {
		t.Fatal("foreign controller stop was acknowledged without persisted host proof")
	}
}
