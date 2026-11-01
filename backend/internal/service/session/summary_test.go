package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestToSessionWithFactsCarriesCardSummary(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:        "sum-4",
		ProjectID: "sum",
		UpdatedAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
	}
	rec.Metadata.CardSummary = "Adding retry logic to checkout flow"
	sess, err := (&Service{store: st, clock: func() time.Time { return rec.UpdatedAt.Add(2 * time.Minute) }}).toSessionWithFacts(context.Background(), rec, nil, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Summary != "Adding retry logic to checkout flow" {
		t.Fatalf("summary = %q, want the agent's card summary", sess.Summary)
	}
}

func TestToSessionWithFactsLeavesSummaryEmptyUntilTheAgentSetsOne(t *testing.T) {
	t.Parallel()
	rec := domain.SessionRecord{ID: "sum-5", ProjectID: "sum", UpdatedAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)}
	sess, err := (&Service{store: newFakeStore(), clock: func() time.Time { return rec.UpdatedAt }}).toSessionWithFacts(context.Background(), rec, nil, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Summary != "" {
		t.Fatalf("summary = %q, want none before the agent sets one", sess.Summary)
	}
}
