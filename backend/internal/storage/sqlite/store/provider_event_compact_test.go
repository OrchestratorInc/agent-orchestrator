package store_test

import (
	"context"
	"testing"
	"time"
)

func TestCompactProviderEventPayloadsKeepsRowsForReplay(t *testing.T) {
	ctx := context.Background()
	s, raw, session, conversation := archiveBranchFixture(t)
	old := testNow.Add(-10 * 24 * time.Hour)
	for _, event := range []struct {
		id, method string
		at         time.Time
	}{
		{"old-delta", "message.delta", old},
		{"old-done", "turn.completed", old},
		{"new-delta", "message.delta", testNow},
	} {
		if _, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, "generation-root", event.id, event.method,
			`{"text":"payload"}`, event.at, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("ProjectProviderEvent %s: %v", event.id, err)
		}
	}

	n, err := s.CompactProviderEventPayloads(ctx, testNow.Add(-7*24*time.Hour), 100)
	if err != nil || n != 1 {
		t.Fatalf("CompactProviderEventPayloads = %d, %v; want 1 row", n, err)
	}
	for id, want := range map[string]string{"old-delta": "{}", "old-done": `{"text":"payload"}`, "new-delta": `{"text":"payload"}`} {
		var payload string
		if err := raw.QueryRow(`SELECT payload_json FROM conversation_provider_events WHERE provider_event_id = ?`, id).Scan(&payload); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if payload != want {
			t.Fatalf("%s payload = %q, want %q", id, payload, want)
		}
	}

	projected := false
	inserted, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, "generation-root", "old-delta", "message.delta",
		`{"text":"payload"}`, testNow, func(context.Context) error { projected = true; return nil })
	if err != nil || inserted || projected {
		t.Fatalf("replayed old-delta: inserted=%v projected=%v err=%v, want it recognized as already seen", inserted, projected, err)
	}
}
