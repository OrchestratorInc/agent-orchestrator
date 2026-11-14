package chat_test

import (
	"context"
	"encoding/json"
	"testing"

	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// A degraded handoff's boundary notice is a timeline row, not a prompt: the
// agent never receives it, and an outbox redelivery settles the same row.
func TestRecordInterfaceTransitionBoundaryAddsOneTimelineRowAndNoPrompt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const notice = "Chat started fresh — the terminal's native history stays in Terminal."
	for range 2 {
		if err := h.svc.RecordInterfaceTransitionBoundary(ctx, testSession, "transition-1", notice); err != nil {
			t.Fatalf("RecordInterfaceTransitionBoundary: %v", err)
		}
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.Summary == notice {
				return true
			}
		}
		return false
	})
	rows := 0
	for _, activity := range snapshot.Activities {
		var detail map[string]string
		_ = json.Unmarshal(activity.Detail, &detail)
		if detail["event"] != chatsvc.InterfaceBoundaryEvent {
			continue
		}
		rows++
		if activity.Summary != notice || activity.TurnID != "" {
			t.Fatalf("boundary row = %+v, want a turnless row with the notice", activity)
		}
	}
	if rows != 1 {
		t.Fatalf("boundary rows = %d, want 1", rows)
	}
	if len(snapshot.Turns) != 0 {
		t.Fatalf("boundary notice created %d turns, want none", len(snapshot.Turns))
	}
	h.conv.mu.Lock()
	sent := len(h.conv.sent)
	h.conv.mu.Unlock()
	if sent != 0 {
		t.Fatalf("boundary notice reached the agent %d times, want 0", sent)
	}
}
