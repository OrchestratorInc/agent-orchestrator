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

	// One row per step: the walk stops at the first event newer than the cutoff.
	cutoff := testNow.Add(-7 * 24 * time.Hour)
	var cursor int64
	steps := 0
	for more := true; more; steps++ {
		next, again, err := s.CompactProviderEventPayloads(ctx, cursor, cutoff, 1)
		if err != nil {
			t.Fatalf("CompactProviderEventPayloads after %d: %v", cursor, err)
		}
		if steps > 5 {
			t.Fatalf("compaction did not stop at the first new event")
		}
		cursor, more = next, again
	}
	var newID int64
	if err := raw.QueryRow(`SELECT id FROM conversation_provider_events WHERE provider_event_id = 'new-delta'`).Scan(&newID); err != nil {
		t.Fatalf("read new-delta id: %v", err)
	}
	if cursor == 0 || cursor >= newID {
		t.Fatalf("cursor = %d, want it past the old events and before new-delta (%d)", cursor, newID)
	}
	if again, more, err := s.CompactProviderEventPayloads(ctx, cursor, cutoff, 1); err != nil || again != cursor || more {
		t.Fatalf("second pass = %d, %v, %v; want no progress until new-delta ages", again, more, err)
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
