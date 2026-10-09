package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestDeriveSummaryUsesAIGeneratedSummary(t *testing.T) {
	t.Parallel()
	rec := domain.SessionRecord{ID: "sum-1", ProjectID: "sum"}
	rec.Metadata.LatestAssistantUpdate = domain.CardSummaryMetadataPrefix + "Refactoring auth middleware to use JWT"
	got := deriveSummary(rec, nil, contract.DisplayWorking)
	if got != "Refactoring auth middleware to use JWT" {
		t.Fatalf("summary = %q, want AI-generated summary", got)
	}
}

func TestDeriveSummaryFallsBackToWorkingOrStalled(t *testing.T) {
	t.Parallel()
	rec := domain.SessionRecord{ID: "sum-2", ProjectID: "sum"}
	if got := deriveSummary(rec, nil, contract.DisplayWorking); got != "Working" {
		t.Fatalf("summary = %q, want Working", got)
	}
	if got := deriveSummary(rec, nil, contract.DisplayTerminated); got != "Stalled" {
		t.Fatalf("summary = %q, want Stalled", got)
	}
}

func TestToSessionWithFactsDerivesSummary(t *testing.T) {
	t.Parallel()
	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:        "sum-4",
		ProjectID: "sum",
		UpdatedAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
	}
	rec.Metadata.LatestAssistantUpdate = domain.CardSummaryMetadataPrefix + "Adding retry logic to checkout flow"
	sess, err := (&Service{store: st, clock: func() time.Time { return rec.UpdatedAt.Add(2 * time.Minute) }}).toSessionWithFacts(context.Background(), rec, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Summary != "Adding retry logic to checkout flow" {
		t.Fatalf("summary = %q, want AI-generated summary", sess.Summary)
	}
}
