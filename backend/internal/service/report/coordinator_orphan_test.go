package report

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCoordinatorUnavailableProjectDoesNotBlockActiveProject(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "retired-manager", ProjectID: "p", Kind: domain.KindOrchestrator, IsTerminated: true},
		{ID: "active-manager", ProjectID: "q", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
	}
	orphan := reportAt("older", "retired-worker", now.Add(-time.Minute), now.Add(-time.Minute), "historical")
	active := reportAt("newer", "active-worker", now, now, "candidate completed")
	active.ProjectID = "q"
	store.add(orphan, active)
	delivery := &coordinatorDelivery{}
	coordinator := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})

	delay, err := coordinator.RunDue(ctx)
	if err != nil || delay != OrchestratorBusyRetry {
		t.Fatalf("first pass delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "active-manager" || store.acked != 1 {
		t.Fatalf("first pass submissions = %#v, acknowledged = %d", delivery.submissions, store.acked)
	}
	retained := store.reports[0]
	if retained.DeliveryState != domain.ReportPending || retained.DeliveryBatchID == "" || retained.DeliveryAttempts != 0 ||
		!retained.AvailableAt.Equal(now.Add(delay)) || !retained.AcknowledgedAt.IsZero() {
		t.Fatalf("orphan outcome = %#v, want a deferred, unacknowledged batch", retained)
	}

	now = now.Add(delay)
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	retried := store.reports[0]
	if retried.DeliveryAttempts != 0 || retried.DeliveryBatchID != retained.DeliveryBatchID {
		t.Fatalf("retry = %#v, want the same batch without a counted attempt", retried)
	}

	store.sessions = append(store.sessions, domain.SessionRecord{
		ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
	})
	now = now.Add(delay)
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 2 || delivery.submissions[1].target != "replacement" ||
		delivery.submissions[1].key != retained.DeliveryBatchID || store.acked != 2 {
		t.Fatalf("final submissions = %#v, acknowledged = %d", delivery.submissions, store.acked)
	}
}
