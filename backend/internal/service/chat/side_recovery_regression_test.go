package chat_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestSideLaunchRecoveryRetryDoesNotDuplicateProvidersOrReopenClosedSides(t *testing.T) {
	h := newSideWorkflow(t, "")
	ctx := context.Background()
	conv := newFakeConversation()
	conv.providerConversationID = "recovered-fork"
	var resumes atomic.Int32
	h.driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) {
		resumes.Add(1)
		return conv, nil
	}
	records := []chatsvc.SideRecoveryRecord{{
		PolicyVersion: 1, BoundaryState: "delivered", BoundaryTurnID: "boundary",
		Harness: domain.HarnessCodex, Generation: "recovered-generation",
		ProviderHostID: "btw-workflow-recovered", ProviderForkID: "recovered-fork",
		LaunchConfig: json.RawMessage(`{"WorkspacePath":"recovery-workspace"}`),
		Side:         domain.SideConversation{ID: "recovered", SessionID: testSession, MainConversationID: h.ctrl.ConversationID(), State: "ready"},
	}}
	for range 10 {
		if err := h.svc.ClaimSideChatLaunch(ctx, "workflow"); err != nil {
			t.Fatal(err)
		}
		if err := h.svc.RecoverSideChatLaunch(ctx, "workflow", records); err != nil {
			t.Fatal(err)
		}
	}
	h.awaitSide(t, "recovered", func(s domain.SideSnapshot) bool { return s.Side.State == "ready" })
	if resumes.Load() != 1 {
		t.Fatalf("resumed %d providers", resumes.Load())
	}
	if err := h.svc.CloseIndependentSideChat(ctx, testSession, "recovered"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.RecoverSideChatLaunch(ctx, "workflow", records); err != nil {
		t.Fatal(err)
	}
	sides, err := h.svc.ListIndependentSideChats(ctx, testSession)
	if err != nil || len(sides) != 0 || resumes.Load() != 1 {
		t.Fatalf("closed side restored: %+v, resumes=%d, err=%v", sides, resumes.Load(), err)
	}
}

type terminalSideHistory struct {
	*fakeConversation
	history []ports.ChatEvent
}

func (c *terminalSideHistory) ReadHistory(context.Context) ([]ports.ChatEvent, error) {
	return c.history, nil
}

// Exercise the public recovery boundary, not just the history reconciliation
// helper: Electron's last snapshot predates an already-delivered completion.
func TestSideLaunchRecoveryReconcilesStaleRunningSnapshot(t *testing.T) {
	for _, completed := range []bool{true, false} {
		name := "unknown outcome holds queue"
		if completed {
			name = "completed outcome releases queue without replay"
		}
		t.Run(name, func(t *testing.T) {
			h := newSideWorkflow(t, "")
			ctx := context.Background()
			conv := &terminalSideHistory{fakeConversation: newFakeConversation()}
			conv.providerConversationID = "recovered-fork"
			if completed {
				conv.history = []ports.ChatEvent{{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "already-delivered", TurnState: domain.TurnStateCompleted}}
			}
			h.driver.resume = func(ports.ChatResumeConfig) (ports.ChatConversation, error) { return conv, nil }
			now := time.Now().UTC()
			records := []chatsvc.SideRecoveryRecord{{
				PolicyVersion: 1, BoundaryState: "delivered", BoundaryTurnID: "boundary",
				Harness: domain.HarnessCodex, Generation: "recovered-generation",
				ProviderHostID: "btw-workflow-recovered", ProviderForkID: "recovered-fork",
				LaunchConfig: json.RawMessage(`{"WorkspacePath":"recovery-workspace"}`),
				Side:         domain.SideConversation{ID: "recovered", SessionID: testSession, MainConversationID: h.ctrl.ConversationID(), State: "ready"},
				Turns: []chatsvc.SideRecoveryTurn{
					{ProviderTurnID: "already-delivered", Turn: domain.SideTurn{ID: "old", SideID: "recovered", ClientMessageID: "old-receipt", Text: "original question", State: "running", CreatedAt: now}},
					{Turn: domain.SideTurn{ID: "next", SideID: "recovered", ClientMessageID: "next-receipt", Text: "queued question", State: "queued", CreatedAt: now.Add(time.Second)}},
				},
			}}
			// Match the real JSON transport between Electron and a replacement daemon.
			raw, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &records); err != nil {
				t.Fatal(err)
			}
			if err := h.svc.RecoverSideChatLaunch(ctx, "workflow", records); err != nil {
				t.Fatal(err)
			}
			if completed {
				h.awaitSide(t, "recovered", func(s domain.SideSnapshot) bool {
					return len(s.Turns) == 2 && s.Turns[0].State == "completed" && s.Turns[1].State == "running"
				})
				for deadline := time.Now().Add(time.Second); len(conv.sentTexts()) == 0 && time.Now().Before(deadline); {
					time.Sleep(time.Millisecond)
				}
				if sent := conv.sentTexts(); len(sent) != 1 || sent[0] != "queued question" {
					t.Fatalf("provider received %v", sent)
				}
			} else {
				snapshot := h.awaitSide(t, "recovered", func(s domain.SideSnapshot) bool { return s.Side.State == "failed" })
				if snapshot.Side.ErrorMessage == "" || snapshot.Turns[1].State != "queued" {
					t.Fatalf("unknown recovery = %+v", snapshot)
				}
				if sent := conv.sentTexts(); len(sent) != 0 {
					t.Fatalf("unverified work replayed: %v", sent)
				}
			}
		})
	}
}
