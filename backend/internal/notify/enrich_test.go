package notify

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichOrphanWorkersCarriesDetailWithoutPR(t *testing.T) {
	t.Parallel()

	detail := "1 orphaned worker process tree of terminated sessions: dead-1."
	rec, err := enrich(Intent{
		Type:      domain.NotificationOrphanWorkers,
		SessionID: "dead-1",
		SourceKey: "orphan_workers:run-1",
		Detail:    detail,
		CreatedAt: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich orphan notification: %v", err)
	}
	if want := "Orphaned worker processes found"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if rec.Body != detail {
		t.Fatalf("body = %q, want the intent detail verbatim", rec.Body)
	}
	// Same shape without a PR URL must stay valid for the session-bound kinds'
	// gate; the orphan intent needs no PR at all.
	if rec.PRURL != "" {
		t.Fatalf("pr url = %q, want empty", rec.PRURL)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}
