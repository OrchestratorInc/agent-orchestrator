package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// Older daemons stored a replayed provider notice ("You've hit your 5-hour
// limit…") as a running row on a turn that had already settled, so every client
// showed a spinner that never stopped. Starting the chat settles those rows the
// way their turn's settlement would have, and leaves finished rows alone.
func TestSettleStrandedActivitiesMatchesTurnSettlement(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	notice := "You've hit your 5-hour limit · resets 4:50pm (Asia/Calcutta)"
	later := histClock.Add(time.Hour)

	// Every settled turn state, so the SQL CASE cannot drift from terminalActivityStatus.
	states := []domain.TurnState{
		domain.TurnStateCompleted,
		domain.TurnStateRecovered,
		domain.TurnStateInterrupted,
		domain.TurnStateFailed,
		domain.TurnStateCancelled,
	}
	for _, state := range states {
		turnID := "turn-" + string(state)
		providerTurnID := "provider-" + turnID
		if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{
			ID: turnID + "-msg", Text: "do the work", Origin: domain.MessageOriginHuman,
		}, turnID, histClock); err != nil || !created {
			t.Fatalf("append prompt for %s: created=%v err=%v", turnID, created, err)
		}
		if err := s.BindTurnToProvider(ctx, turnID, providerTurnID, histClock); err != nil {
			t.Fatalf("bind %s: %v", turnID, err)
		}
		if err := s.SettleTurn(ctx, conversation, providerTurnID, state, "", histClock); err != nil {
			t.Fatalf("settle %s: %v", turnID, err)
		}
		// Rows that arrive after their turn settled, as a history replay stored them.
		for _, row := range []struct {
			suffix string
			status domain.ActivityStatus
		}{{"stranded", domain.ActivityStatusRunning}, {"finished", domain.ActivityStatusCompleted}} {
			id := row.suffix + "-" + string(state)
			if err := s.UpsertActivity(ctx, conversation, providerTurnID, domain.ConversationActivity{
				ID: id, Kind: domain.ActivityKindSystem, Status: row.status, Summary: notice,
				ProviderItemID: "session-failure:" + id,
				Detail:         []byte(`{"event":"provider.failure","category":"limit"}`),
			}, later); err != nil {
				t.Fatalf("upsert %s: %v", id, err)
			}
		}
	}
	turns := turnStates(t, s, conversation)
	before := activitiesByID(t, s, conversation)

	if err := s.SettleStrandedActivities(ctx, session, later.Add(time.Minute)); err != nil {
		t.Fatalf("SettleStrandedActivities: %v", err)
	}

	after := activitiesByID(t, s, conversation)
	status := func(id string) domain.ActivityStatus {
		t.Helper()
		// Read the row itself: the snapshot leaves out a cancelled turn's items.
		got, found, err := s.ActivityStatusByProviderItem(ctx, conversation, "session-failure:"+id)
		if err != nil || !found {
			t.Fatalf("read %s: found=%v err=%v", id, found, err)
		}
		return got
	}
	for _, state := range states {
		if got := turns["turn-"+string(state)]; got != state {
			t.Fatalf("turn settled as %q, want %q", got, state)
		}
		want := store.TerminalActivityStatus(state)
		if got := status("stranded-" + string(state)); got != want {
			t.Errorf("row stranded on a %s turn = %q, want %q (terminalActivityStatus)", state, got, want)
		}
		id := "finished-" + string(state)
		if got := status(id); got != domain.ActivityStatusCompleted {
			t.Errorf("finished row on a %s turn = %q, want it left completed", state, got)
		}
		if shown, ok := before[id]; ok && after[id].Revision != shown.Revision {
			t.Errorf("finished row on a %s turn was rewritten: revision %d -> %d", state, shown.Revision, after[id].Revision)
		}
	}
}

func activitiesByID(t *testing.T, s *sqlite.Store, conversation string) map[string]domain.ConversationActivity {
	t.Helper()
	snapshot, err := s.LoadConversationSnapshot(context.Background(), conversation)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	byID := make(map[string]domain.ConversationActivity, len(snapshot.Activities))
	for _, activity := range snapshot.Activities {
		byID[activity.ID] = activity
	}
	return byID
}

func turnStates(t *testing.T, s *sqlite.Store, conversation string) map[string]domain.TurnState {
	t.Helper()
	snapshot, err := s.LoadConversationSnapshot(context.Background(), conversation)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	states := make(map[string]domain.TurnState, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		states[turn.ID] = turn.State
	}
	return states
}
