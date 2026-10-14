package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUpsertActivityPersistsAOActionOnMigratedStore(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	ctx := context.Background()

	if err := s.UpsertActivity(ctx, conversation, "", domain.ConversationActivity{
		ID:             "act-command",
		Kind:           domain.ActivityKindCommand,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "git status",
		ProviderItemID: "exec-1",
	}, histClock); err != nil {
		t.Fatalf("upsert command: %v", err)
	}
	activity := domain.ConversationActivity{
		ID:             "act-ao",
		Kind:           domain.ActivityKindAOAction,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Terminated an agent",
		Detail:         []byte(`{"action":"session.terminated"}`),
		ProviderItemID: "ao-action:session.terminated:mer-1:3",
	}
	if err := s.UpsertActivity(ctx, conversation, "", activity, histClock); err != nil {
		t.Fatalf("upsert ao_action: %v", err)
	}
	activity.Status = domain.ActivityStatusFailed
	activity.Summary = "Terminated an agent failed"
	if err := s.UpsertActivity(ctx, conversation, "", activity, histClock.Add(1)); err != nil {
		t.Fatalf("settle ao_action: %v", err)
	}

	snapshot, err := s.LoadConversationSnapshot(ctx, conversation)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	var command, action int
	for _, item := range snapshot.Activities {
		switch item.Kind {
		case domain.ActivityKindCommand:
			command++
		case domain.ActivityKindAOAction:
			action++
			if item.Status != domain.ActivityStatusFailed || item.ProviderItemID != activity.ProviderItemID {
				t.Fatalf("settled activity = %+v", item)
			}
		}
	}
	if command != 1 || action != 1 {
		t.Fatalf("activities = %+v, want one command and one settled ao_action", snapshot.Activities)
	}
}
