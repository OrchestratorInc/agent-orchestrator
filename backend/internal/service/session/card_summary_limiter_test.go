package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeCardSummaryStore struct {
	writes []string
	found  bool
}

func (f *fakeCardSummaryStore) UpdateSessionCardSummary(_ context.Context, _ domain.SessionID, summary string) (bool, error) {
	if f.found {
		f.writes = append(f.writes, summary)
	}
	return f.found, nil
}

func TestCardSummaryLimiter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := &fakeCardSummaryStore{found: true}
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	l := NewCardSummaryLimiter(store, func() time.Time { return now })
	update := func(summary string, done bool) time.Duration {
		t.Helper()
		found, wait, err := l.Update(ctx, "ao-1", summary, done)
		if err != nil || !found {
			t.Fatalf("Update(%q) found=%v err=%v", summary, found, err)
		}
		return wait
	}

	if wait := update("Reading the board code", false); wait != 0 {
		t.Fatalf("first summary held back for %v", wait)
	}
	now = now.Add(time.Minute)
	if wait := update("Reading the board code", false); wait != 0 {
		t.Fatalf("repeat of the accepted text held back for %v", wait)
	}
	if wait := update("Adding the summary field", false); wait != 2*time.Minute {
		t.Fatalf("early summary wait = %v, want 2m", wait)
	}
	now = now.Add(2 * time.Minute)
	if wait := update("Adding the summary field", false); wait != 0 {
		t.Fatalf("summary after the interval held back for %v", wait)
	}
	if wait := update("Added card summaries", true); wait != 0 {
		t.Fatalf("final summary held back for %v", wait)
	}
	want := []string{"Reading the board code", "Adding the summary field", "Added card summaries"}
	if len(store.writes) != len(want) {
		t.Fatalf("writes = %q, want %q", store.writes, want)
	}
	for i := range want {
		if store.writes[i] != want[i] {
			t.Fatalf("writes = %q, want %q", store.writes, want)
		}
	}

	store.found = false
	if found, _, err := l.Update(ctx, "missing", "Fixing a bug", false); err != nil || found {
		t.Fatalf("unknown session: found=%v err=%v", found, err)
	}
	store.found = true
	if found, wait, _ := l.Update(ctx, "missing", "Fixing a bug", false); !found || wait != 0 {
		t.Fatalf("a failed write must not start the interval: found=%v wait=%v", found, wait)
	}
}
