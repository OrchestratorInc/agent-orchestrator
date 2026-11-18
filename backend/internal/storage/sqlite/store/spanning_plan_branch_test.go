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

	// The original branch writes the list on its first turn.
	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-a", "provider-turn-a", testNow)
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
	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-b", "provider-turn-b", testNow.Add(2*time.Minute))
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
	if planRowSuperseded(t, original, "turn-a") {
		t.Fatalf("back on the original branch, turn-a's plan row is still superseded")
	}
}

// Editing a message forks and activates a branch in one step. Its path drops the
// copies written after the fork, so the copy before it is current again; left
// superseded, the edited branch shows no plan until the agent revises the list.
func TestSpanningPlanIsCurrentOnABranchForkedByAnEdit(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)

	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-a", "provider-turn-a", testNow)
	forkAfter := latestSequence(t, s, conversation.ID)
	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-b", "provider-turn-b", testNow.Add(time.Minute))
	if !planOf(t, loadSnapshot(t, s, conversation.ID), "turn-a").Superseded {
		t.Fatalf("before the edit, want turn-a's copy superseded by turn-b's")
	}

	branch := domain.ConversationBranch{
		ID: "branch-edited", ConversationID: conversation.ID, ProviderConversationID: "thread-edited",
		ParentBranchID: conversation.ActiveBranchID, ForkAfterTurnID: "turn-a", ForkAfterSequence: forkAfter,
	}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "generation-edited", testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("CreateAndActivateConversationBranch: %v", err)
	}
	edited := loadSnapshot(t, s, conversation.ID)
	if planOf(t, edited, "turn-a").Superseded {
		t.Fatalf("on the edited branch, turn-a's plan is the only copy but is still superseded")
	}
	if planRowSuperseded(t, edited, "turn-a") {
		t.Fatalf("on the edited branch, turn-a's plan row is the only copy but is still superseded")
	}
}

// Startup undoes an edit whose branch never recorded its prompt, moving the head
// back to the source branch. That path holds the copies written after the fork
// again, so the latest of them is current, not the copy the edit made current.
func TestSpanningPlanIsCurrentAgainAfterAnAbandonedEditIsRepaired(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	root := conversation.ActiveBranchID

	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-a", "provider-turn-a", testNow)
	forkAfter := latestSequence(t, s, conversation.ID)
	writeSpanningPlan(t, s, session.ID, conversation.ID, "turn-b", "provider-turn-b", testNow.Add(time.Minute))

	branch := domain.ConversationBranch{
		ID: "branch-edited", ConversationID: conversation.ID, ProviderConversationID: "thread-edited",
		ParentBranchID: root, ForkAfterTurnID: "turn-a", ForkAfterSequence: forkAfter, ReplacedTurnID: "turn-b",
	}
	if err := s.CreateAndActivateConversationBranch(ctx, session.ID, branch, "generation-edited", testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("CreateAndActivateConversationBranch: %v", err)
	}
	repaired, _, err := s.RepairIncompleteConversationEdit(ctx, session.ID, conversation.ID, testNow.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("RepairIncompleteConversationEdit: %v", err)
	}
	if repaired.ID != root {
		t.Fatalf("repair restored branch %q, want source %q", repaired.ID, root)
	}
	restored := loadSnapshot(t, s, conversation.ID)
	if !planOf(t, restored, "turn-a").Superseded || !planRowSuperseded(t, restored, "turn-a") {
		t.Fatalf("after the repair, turn-a's copy is current beside turn-b's; want it superseded")
	}
	if planOf(t, restored, "turn-b").Superseded || planRowSuperseded(t, restored, "turn-b") {
		t.Fatalf("after the repair, turn-b's copy is superseded; want it current")
	}
}

// writeSpanningPlan starts a turn and writes its copy of a list that spans turns,
// the way the controller does: the turn's plan, its plan row, then a reconcile.
func writeSpanningPlan(t *testing.T, s *sqlite.Store, sessionID domain.SessionID, conversationID, turnID, providerTurnID string, written time.Time) {
	t.Helper()
	ctx := context.Background()
	created, err := s.AppendUserMessage(ctx, conversationID, sessionID, generationOf(t, s, sessionID), domain.ConversationMessage{
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
	if _, err := s.SetTurnPlan(ctx, conversationID, providerTurnID, plan); err != nil {
		t.Fatalf("SetTurnPlan %s: %v", turnID, err)
	}
	detail, _ := json.Marshal(map[string]any{
		"event": "plan", "spansTurns": true, "writtenAt": written,
		"steps": []map[string]string{{"text": "Render " + turnID, "status": "in_progress"}},
	})
	if err := s.UpsertActivity(ctx, conversationID, providerTurnID, domain.ConversationActivity{
		ID: "plan-" + turnID, Kind: domain.ActivityKindPlan, Status: domain.ActivityStatusRunning,
		Detail: detail, ProviderItemID: "ao-plan-" + providerTurnID,
	}, written); err != nil {
		t.Fatalf("UpsertActivity %s: %v", turnID, err)
	}
	if err := s.ReconcileSpanningPlans(ctx, conversationID, written); err != nil {
		t.Fatalf("ReconcileSpanningPlans after %s: %v", turnID, err)
	}
}

func planRowSuperseded(t *testing.T, snapshot store.ConversationSnapshot, turnID string) bool {
	t.Helper()
	for _, activity := range snapshot.Activities {
		if activity.Kind == domain.ActivityKindPlan && activity.TurnID == turnID {
			var detail struct{ Superseded bool }
			_ = json.Unmarshal(activity.Detail, &detail)
			return detail.Superseded
		}
	}
	t.Fatalf("no plan row on %s in snapshot of %d activities", turnID, len(snapshot.Activities))
	return false
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
