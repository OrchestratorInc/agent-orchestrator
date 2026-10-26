package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestConversationActivityReadsOneFullRow(t *testing.T) {
	s, _, conversation := conversationFixture(t)
	ctx := context.Background()

	if err := s.UpsertActivity(ctx, conversation, "", domain.ConversationActivity{
		ID:             "act-command",
		Kind:           domain.ActivityKindCommand,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "go test ./...",
		Detail:         []byte(`{"command":"go test ./...","output":"ok\n","exitCode":0}`),
		ProviderItemID: "exec-1",
	}, histClock); err != nil {
		t.Fatalf("upsert command: %v", err)
	}

	activity, err := s.ConversationActivity(ctx, conversation, "act-command")
	if err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if activity.ID != "act-command" || activity.Summary != "go test ./..." || string(activity.Detail) != `{"command":"go test ./...","output":"ok\n","exitCode":0}` {
		t.Fatalf("activity = %+v, detail = %s", activity, activity.Detail)
	}

	if _, err := s.ConversationActivity(ctx, conversation, "act-missing"); !errors.Is(err, domain.ErrNoConversationActivity) {
		t.Fatalf("missing activity err = %v, want ErrNoConversationActivity", err)
	}
	if _, err := s.ConversationActivity(ctx, "other-conversation", "act-command"); !errors.Is(err, domain.ErrNoConversationActivity) {
		t.Fatalf("activity from another conversation err = %v, want ErrNoConversationActivity", err)
	}
}
