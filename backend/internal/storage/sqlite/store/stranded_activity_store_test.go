package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Older daemons stored a replayed provider notice ("You've hit your 5-hour
// limit…") as a running row on a turn that had already settled, so every client
// showed a spinner that never stopped. Starting the chat again settles those rows
// the way their turn's settlement would have, and leaves finished rows alone.
func TestSettleOrphanedTurnsSettlesRowsStrandedOnSettledTurns(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	limit := "You've hit your 5-hour limit · resets 4:50pm (Asia/Calcutta)"

	settled := func(turnID string, state domain.TurnState, errMessage string) string {
		t.Helper()
		providerTurnID := "provider-" + turnID
		if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1", domain.ConversationMessage{
			ID: turnID + "-msg", Text: "do the work", Origin: domain.MessageOriginHuman,
		}, turnID, histClock); err != nil || !created {
			t.Fatalf("append prompt for %s: created=%v err=%v", turnID, created, err)
		}
		if err := s.BindTurnToProvider(ctx, turnID, providerTurnID, histClock); err != nil {
			t.Fatalf("bind %s: %v", turnID, err)
		}
		if err := s.SettleTurn(ctx, conversation, providerTurnID, state, errMessage, histClock); err != nil {
			t.Fatalf("settle %s: %v", turnID, err)
		}
		return providerTurnID
	}
	failedTurn := settled("turn-limited", domain.TurnStateFailed, limit)
	completedTurn := settled("turn-done", domain.TurnStateCompleted, "")

	// Rows that arrive after their turn settled, as a history replay stored them.
	later := histClock.Add(time.Hour)
	for _, row := range []struct {
		id, providerTurnID string
		status             domain.ActivityStatus
	}{
		{"stranded-limit", failedTurn, domain.ActivityStatusRunning},
		{"stranded-retry", completedTurn, domain.ActivityStatusRunning},
		{"finished-note", completedTurn, domain.ActivityStatusCompleted},
	} {
		if err := s.UpsertActivity(ctx, conversation, row.providerTurnID, domain.ConversationActivity{
			ID: row.id, Kind: domain.ActivityKindSystem, Status: row.status, Summary: limit,
			ProviderItemID: "session-failure:" + row.id,
			Detail:         []byte(`{"event":"provider.failure","category":"limit"}`),
		}, later); err != nil {
			t.Fatalf("upsert %s: %v", row.id, err)
		}
	}
	before := activitiesByID(t, s, conversation)

	if err := s.SettleOrphanedTurns(ctx, session, later.Add(time.Minute)); err != nil {
		t.Fatalf("SettleOrphanedTurns: %v", err)
	}

	after := activitiesByID(t, s, conversation)
	if got := after["stranded-limit"].Status; got != domain.ActivityStatusFailed {
		t.Fatalf("row stranded on a failed turn = %q, want failed", got)
	}
	if got := after["stranded-retry"].Status; got != domain.ActivityStatusCompleted {
		t.Fatalf("row stranded on a completed turn = %q, want completed", got)
	}
	if after["finished-note"].Revision != before["finished-note"].Revision {
		t.Fatalf("finished row was rewritten: revision %d -> %d", before["finished-note"].Revision, after["finished-note"].Revision)
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
