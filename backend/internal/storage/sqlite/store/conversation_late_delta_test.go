package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAssistantDeltaKeepsTerminalTurnSettled(t *testing.T) {
	for _, state := range []domain.TurnState{
		domain.TurnStateCompleted, domain.TurnStateRecovered, domain.TurnStateInterrupted,
		domain.TurnStateFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			const providerTurn = "provider-turn"
			if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "turn", providerTurn, histClock); err != nil {
				t.Fatal(err)
			}
			if err := s.AppendAssistantDelta(ctx, conversation, "existing", providerTurn, "before", "message", histClock); err != nil {
				t.Fatal(err)
			}
			if err := s.SettleTurn(ctx, conversation, providerTurn, state, "", histClock.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			for _, item := range []string{"existing", "first-seen-after-stop"} {
				if err := s.AppendAssistantDelta(ctx, conversation, item, providerTurn, " late", item+"-message", histClock.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Messages) != 2 || len(snapshot.Turns) != 1 || snapshot.Turns[0].State != state {
				t.Fatalf("late output changed the turn or message count: %+v", snapshot)
			}
			for _, message := range snapshot.Messages {
				want := " late"
				if message.ProviderItemID == "existing" {
					want = "before late"
				}
				if message.Text != want || message.Streaming || message.TurnID != "turn" {
					t.Errorf("late message = %+v, want text %q on settled turn", message, want)
				}
			}
		})
	}
}

func TestAssistantDeltaStillStreamsActiveAndUnboundTurns(t *testing.T) {
	for _, providerTurn := range []string{"running", "unknown", ""} {
		t.Run("turn="+providerTurn, func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "turn", "running", histClock); err != nil {
				t.Fatal(err)
			}
			for _, delta := range []string{"first", " second"} {
				if err := s.AppendAssistantDelta(ctx, conversation, "item", providerTurn, delta, "message", histClock); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != "first second" || !snapshot.Messages[0].Streaming {
				t.Fatalf("active or unbound output stopped streaming: %+v", snapshot.Messages)
			}
		})
	}
}
