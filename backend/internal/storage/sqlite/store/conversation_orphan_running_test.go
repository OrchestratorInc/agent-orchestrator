package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An agent that died mid-turn leaves that turn running. When a message queued
// for its restart is waiting too, the restart settles only the orphaned turn;
// the queued message is what the restart is for and must survive it.
func TestSettleOrphanedRunningTurnsKeepsQueuedMessages(t *testing.T) {
	for _, tc := range []struct {
		name        string
		runningOnly bool
		wantQueued  domain.TurnState
	}{
		{name: "restart keeps the queue", runningOnly: true, wantQueued: domain.TurnStateQueued},
		{name: "ordinary start settles both", runningOnly: false, wantQueued: domain.TurnStateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, session, conversation := conversationFixture(t)
			ctx := context.Background()
			if err := s.AdoptProviderTurn(ctx, conversation, session, "gen-1", "orphan", "provider-orphan", histClock); err != nil {
				t.Fatal(err)
			}
			if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1",
				domain.ConversationMessage{ID: "message-report", Text: "report", ClientMessageID: "report-batch-1",
					Origin: domain.MessageOriginAutomation}, "queued-report", histClock.Add(time.Second)); err != nil || !created {
				t.Fatalf("append queued message: created=%v err=%v", created, err)
			}

			settle := s.SettleOrphanedTurns
			if tc.runningOnly {
				settle = s.SettleOrphanedRunningTurns
			}
			if err := settle(ctx, session, histClock.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}

			snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			states := map[string]domain.TurnState{}
			for _, turn := range snapshot.Turns {
				states[turn.ID] = turn.State
			}
			if states["orphan"] != domain.TurnStateFailed {
				t.Fatalf("orphaned running turn = %q, want failed (turns %v)", states["orphan"], states)
			}
			if states["queued-report"] != tc.wantQueued {
				t.Fatalf("queued message turn = %q, want %q (turns %v)", states["queued-report"], tc.wantQueued, states)
			}
		})
	}
}
