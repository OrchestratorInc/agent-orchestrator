package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestWithDispatchingTurnRunning(t *testing.T) {
	turns := []domain.ConversationTurn{
		{ID: "ahead", State: domain.TurnStateRunning},
		{ID: "dispatching", State: domain.TurnStateQueued},
		{ID: "waiting", State: domain.TurnStateQueued},
	}

	got := withDispatchingTurnRunning(turns, "dispatching")

	if got[1].State != domain.TurnStateRunning {
		t.Fatalf("dispatching turn state = %s, want running", got[1].State)
	}
	if got[2].State != domain.TurnStateQueued {
		t.Fatalf("waiting turn state = %s, want queued", got[2].State)
	}
	if turns[1].State != domain.TurnStateQueued {
		t.Fatal("input turns were mutated")
	}
	if same := withDispatchingTurnRunning(turns, ""); &same[0] != &turns[0] {
		t.Fatal("no dispatching turn should return the input unchanged")
	}
}

func TestWithDispatchingTurnRunningOverlaysEveryDispatchingTurn(t *testing.T) {
	turns := []domain.ConversationTurn{
		{ID: "a", State: domain.TurnStateQueued},
		{ID: "b", State: domain.TurnStateQueued},
		{ID: "c", State: domain.TurnStateQueued},
	}

	got := withDispatchingTurnRunning(turns, "a", "", "c")

	want := []domain.TurnState{domain.TurnStateRunning, domain.TurnStateQueued, domain.TurnStateRunning}
	for i := range want {
		if got[i].State != want[i] {
			t.Fatalf("turn %s state = %s, want %s", got[i].ID, got[i].State, want[i])
		}
	}
}

func TestWithDispatchingTurnRunningLeavesSettledTurnsAlone(t *testing.T) {
	turns := []domain.ConversationTurn{{ID: "done", State: domain.TurnStateCompleted}}

	got := withDispatchingTurnRunning(turns, "done")

	if got[0].State != domain.TurnStateCompleted {
		t.Fatalf("settled turn state = %s, want completed", got[0].State)
	}
}
