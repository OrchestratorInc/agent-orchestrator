package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A batched queued turn is left out of the snapshot's turns, so the snapshot has
// to carry where it was delivered: with a drain's other members, or by a steer.
func TestSnapshotCarriesBatchedDeliveries(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	appendQueued := func(id string, origin domain.MessageOrigin, at time.Duration) {
		t.Helper()
		if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1",
			domain.ConversationMessage{ID: "message-" + id, Text: id, ClientMessageID: id, Origin: origin}, id, histClock.Add(at)); err != nil || !created {
			t.Fatalf("append %s: created=%v err=%v", id, created, err)
		}
	}

	// Queue drain: a report and a human message start one provider turn together.
	appendQueued("report-1", domain.MessageOriginAutomation, time.Second)
	appendQueued("human-1", domain.MessageOriginHuman, 2*time.Second)
	batch, err := s.ClaimQueuedBatch(ctx, conversation, true, 0, histClock.Add(time.Minute))
	if err != nil || len(batch) != 2 {
		t.Fatalf("claim = %+v, %v", batch, err)
	}
	if err := s.BindTurnToProvider(ctx, batch[0].TurnID, "p-lead", histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteQueuedBatch(ctx, conversation, []string{batch[1].TurnID}, "p-lead", "", histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// A row with no detail at all, as a provider's reasoning step can be, must not
	// break reading the steers.
	reasoning := domain.ConversationActivity{ID: "reasoning-1", Kind: domain.ActivityKindReasoning, Status: domain.ActivityStatusCompleted}
	if err := s.UpsertActivity(ctx, conversation, "p-lead", reasoning, histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Steer: a queued report rides a steer into the running turn.
	appendQueued("report-2", domain.MessageOriginAutomation, 3*time.Second)
	steered, err := s.ClaimQueuedBatch(ctx, conversation, false, 0, histClock.Add(2*time.Minute))
	if err != nil || len(steered) != 1 {
		t.Fatalf("claim steer batch = %+v, %v", steered, err)
	}
	detail, _ := json.Marshal(map[string]any{"event": "steer", "text": "also this", "providerInputSourceTurnIds": []string{steered[0].TurnID}})
	steer := domain.ConversationActivity{ID: "steer-1", Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted, Summary: "Steer", Detail: detail}
	if err := s.UpsertActivity(ctx, conversation, "p-lead", steer, histClock.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteQueuedBatch(ctx, conversation, []string{steered[0].TurnID}, "p-lead", "steer-1", histClock.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	want := map[string]domain.ConversationBatchedDelivery{
		batch[1].TurnID:   {TurnID: batch[1].TurnID, DeliveredWithTurnID: batch[0].TurnID},
		steered[0].TurnID: {TurnID: steered[0].TurnID, DeliveredWithTurnID: batch[0].TurnID, SteerActivityID: "steer-1"},
	}
	full, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]domain.ConversationBatchedDelivery{"full": full.BatchedDeliveries, "page": page.BatchedDeliveries} {
		if len(got) != len(want) {
			t.Fatalf("%s deliveries = %+v", name, got)
		}
		for _, delivery := range got {
			if delivery != want[delivery.TurnID] {
				t.Fatalf("%s delivery = %+v, want %+v", name, delivery, want[delivery.TurnID])
			}
		}
	}
}

// Rolling back does not undo a spawn or a kill, so an AO action filed under a
// rolled-back turn stays in the snapshot while that turn's own work leaves it.
func TestAOActionOutlivesItsRolledBackTurn(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	seedTurn(t, s, conversation, session, "turn-1", "first", histClock)
	seedTurn(t, s, conversation, session, "turn-2", "second", histClock.Add(time.Minute))
	action := domain.ConversationActivity{
		ID: "ao-action:session.spawned:proj-9", Kind: domain.ActivityKindAOAction, Status: domain.ActivityStatusCompleted,
		Summary: "Spawned an agent", ProviderItemID: "ao-action:session.spawned:proj-9",
		Detail: json.RawMessage(`{"action":"session.spawned","operationId":"proj-9"}`),
	}
	if err := s.UpsertActivity(ctx, conversation, "provider-turn-2", action, histClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackTurns(ctx, conversation, "turn-2", histClock.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	full, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.LoadConversationSnapshotPage(ctx, conversation, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, activities := range map[string][]domain.ConversationActivity{"full": full.Activities, "page": page.Activities} {
		ids := make([]string, 0, len(activities))
		for _, activity := range activities {
			ids = append(ids, activity.ID)
		}
		if len(ids) != 2 || ids[0] != "turn-1-act" || ids[1] != action.ID {
			t.Fatalf("%s activities = %v, want turn-1's step and the action", name, ids)
		}
	}
}

// A page carries only the deliveries of batches its own messages belong to, so a
// long orchestrator history does not ride along on every live poll.
func TestPagedSnapshotCarriesOnlyItsOwnBatches(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	const batches = 12
	followers := make([]string, 0, batches)
	for i := range batches {
		at := histClock.Add(time.Duration(i) * time.Minute)
		for _, msg := range []struct {
			id     string
			origin domain.MessageOrigin
		}{{fmt.Sprintf("report-%d", i), domain.MessageOriginAutomation}, {fmt.Sprintf("human-%d", i), domain.MessageOriginHuman}} {
			if created, err := s.AppendUserMessage(ctx, conversation, session, "gen-1",
				domain.ConversationMessage{ID: "message-" + msg.id, Text: msg.id, ClientMessageID: msg.id, Origin: msg.origin}, msg.id, at); err != nil || !created {
				t.Fatalf("append %s: created=%v err=%v", msg.id, created, err)
			}
		}
		batch, err := s.ClaimQueuedBatch(ctx, conversation, true, 0, at)
		if err != nil || len(batch) != 2 {
			t.Fatalf("claim %d = %+v, %v", i, batch, err)
		}
		provider := fmt.Sprintf("p-%d", i)
		if err := s.BindTurnToProvider(ctx, batch[0].TurnID, provider, at); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteQueuedBatch(ctx, conversation, []string{batch[1].TurnID}, provider, "", at); err != nil {
			t.Fatal(err)
		}
		if err := s.SettleTurnByID(ctx, batch[0].TurnID, domain.TurnStateCompleted, "", at); err != nil {
			t.Fatal(err)
		}
		followers = append(followers, batch[1].TurnID)
	}

	full, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil || len(full.BatchedDeliveries) != batches {
		t.Fatalf("full deliveries = %d, %v", len(full.BatchedDeliveries), err)
	}
	// Four messages a page: two batches each, newest first.
	seen := map[string]bool{}
	before := int64(0)
	for page := 0; page < batches/2; page++ {
		snapshot, err := s.LoadConversationSnapshotPage(ctx, conversation, before, 4)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{followers[batches-2*page-1]: true, followers[batches-2*page-2]: true}
		if len(snapshot.BatchedDeliveries) != len(want) {
			t.Fatalf("page %d deliveries = %+v, want %v", page, snapshot.BatchedDeliveries, want)
		}
		for _, delivery := range snapshot.BatchedDeliveries {
			if !want[delivery.TurnID] || seen[delivery.TurnID] {
				t.Fatalf("page %d delivery %+v, want one of %v", page, delivery, want)
			}
			seen[delivery.TurnID] = true
		}
		before = snapshot.OldestSequence
	}
}
