package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// A list that spans turns has one current copy per active path. An edited branch
// that writes its own copy supersedes the copies it can see, the shared ones
// before its fork included; switching back must make the original branch's copy
// current again rather than leave that path with no plan at all.
func TestSpanningPlanStaysCurrentOnTheBranchSwitchedBackTo(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	root := conversation.ActiveBranchID

	writePlan := func(turnID, providerTurnID string, written time.Time) {
		t.Helper()
		created, err := s.AppendUserMessage(ctx, conversation.ID, session.ID, generationOf(t, s, session.ID), domain.ConversationMessage{
			ID: "message-" + turnID, Origin: domain.MessageOriginHuman, Text: "work on " + turnID,
			ClientMessageID: "client-" + turnID,
		}, turnID, written)
		if err != nil || !created {
			t.Fatalf("AppendUserMessage %s: created=%v err=%v", turnID, created, err)
		}
		if err := s.BindTurnToProvider(ctx, turnID, providerTurnID, written); err != nil {
			t.Fatalf("BindTurnToProvider %s: %v", turnID, err)
		}
		plan := domain.ConversationPlan{SpansTurns: true, Steps: []domain.ConversationPlanStep{
			{Text: "Render " + turnID, Status: domain.PlanStepInProgress},
		}}
		if _, err := s.SetTurnPlan(ctx, conversation.ID, providerTurnID, plan); err != nil {
			t.Fatalf("SetTurnPlan %s: %v", turnID, err)
		}
		detail, _ := json.Marshal(map[string]any{
			"event": "plan", "spansTurns": true, "writtenAt": written,
			"steps": []map[string]string{{"text": "Render " + turnID, "status": "in_progress"}},
		})
		if err := s.UpsertActivity(ctx, conversation.ID, providerTurnID, domain.ConversationActivity{
			ID: "plan-" + turnID, Kind: domain.ActivityKindPlan, Status: domain.ActivityStatusRunning,
			Detail: detail, ProviderItemID: "ao-plan-" + providerTurnID,
		}, written); err != nil {
			t.Fatalf("UpsertActivity %s: %v", turnID, err)
		}
		if err := s.ReconcileSpanningPlans(ctx, conversation.ID, written); err != nil {
			t.Fatalf("ReconcileSpanningPlans after %s: %v", turnID, err)
		}
	}

	// The original branch writes the list on its first turn.
	writePlan("turn-a", "provider-turn-a", testNow)
	forkAfter := latestSequence(t, s, conversation.ID)

	// An edited branch forks after that turn and writes its own copy.
	branch := domain.ConversationBranch{
		ID: "branch-edited", ConversationID: conversation.ID, ProviderConversationID: "thread-edited",
		ParentBranchID: root, ForkAfterTurnID: "turn-a", ForkAfterSequence: forkAfter,
	}
	if err := s.CreateConversationBranch(ctx, branch, testNow.Add(time.Minute)); err != nil {
		t.Fatalf("CreateConversationBranch: %v", err)
	}
	if err := s.ActivateConversationBranch(ctx, session.ID, conversation.ID, branch.ID, "thread-edited", "generation-edited", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("ActivateConversationBranch edited: %v", err)
	}
	writePlan("turn-b", "provider-turn-b", testNow.Add(2*time.Minute))
	edited := loadSnapshot(t, s, conversation.ID)
	if !planOf(t, edited, "turn-a").Superseded || planOf(t, edited, "turn-b").Superseded {
		t.Fatalf("on the edited branch, want turn-a's copy superseded and turn-b's current")
	}

	// Back on the original branch, its own copy is the current one again.
	if err := s.ActivateConversationBranch(ctx, session.ID, conversation.ID, root, "thread-root", "generation-root", testNow.Add(3*time.Minute)); err != nil {
		t.Fatalf("ActivateConversationBranch root: %v", err)
	}
	original := loadSnapshot(t, s, conversation.ID)
	if planOf(t, original, "turn-a").Superseded {
		t.Fatalf("back on the original branch, turn-a's plan is still superseded")
	}
	for _, activity := range original.Activities {
		if activity.Kind == domain.ActivityKindPlan && activity.TurnID == "turn-a" {
			var detail struct{ Superseded bool }
			_ = json.Unmarshal(activity.Detail, &detail)
			if detail.Superseded {
				t.Fatalf("back on the original branch, turn-a's plan row is still superseded")
			}
		}
	}
}

func generationOf(t *testing.T, s *sqlite.Store, id domain.SessionID) string {
	t.Helper()
	got, found, err := s.GetSession(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	return got.Metadata.ControllerGeneration
}

func latestSequence(t *testing.T, s *sqlite.Store, conversationID string) int64 {
	t.Helper()
	snapshot := loadSnapshot(t, s, conversationID)
	var latest int64
	for _, message := range snapshot.Messages {
		latest = max(latest, message.Sequence)
	}
	for _, activity := range snapshot.Activities {
		latest = max(latest, activity.Sequence)
	}
	return latest
}

func loadSnapshot(t *testing.T, s *sqlite.Store, conversationID string) store.ConversationSnapshot {
	t.Helper()
	snapshot, err := s.LoadConversationSnapshot(context.Background(), conversationID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	return snapshot
}

func planOf(t *testing.T, snapshot store.ConversationSnapshot, turnID string) domain.ConversationPlan {
	t.Helper()
	for _, turn := range snapshot.Turns {
		if turn.ID == turnID && turn.Plan != nil {
			return *turn.Plan
		}
	}
	t.Fatalf("no plan on %s in snapshot of %d turns", turnID, len(snapshot.Turns))
	return domain.ConversationPlan{}
}
