package report

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An orchestrator whose agent exited (the session still open) used to be
// skipped as "no active orchestrator", so worker reports waited forever. It is
// now the target; delivery restarts its agent.
func TestCoordinatorDeliversToOrchestratorWhoseAgentExited(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{
		ID: "orch-48", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityExited},
	}}
	store.add(reportAt("r1", "worker-67", now, now, "PR opened"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "orch-48" {
		t.Fatalf("submissions = %#v, want one to the exited orchestrator", delivery.submissions)
	}
	if store.acked != 1 {
		t.Fatalf("acknowledged = %d, want 1", store.acked)
	}
}

func TestCoordinatorPrefersRunningOrchestratorOverNewerExitedOne(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "running", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now},
		{ID: "exited", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Minute),
			Activity: domain.Activity{State: domain.ActivityExited}},
		{ID: "terminated", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(2 * time.Minute),
			IsTerminated: true},
	}
	store.add(reportAt("r1", "worker", now, now, "done"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "running" {
		t.Fatalf("submissions = %#v, want the running orchestrator", delivery.submissions)
	}
}

func TestCoordinatorTerminatedOnlyOrchestratorStillHasNoTarget(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{
		ID: "gone", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited},
	}}
	store.add(reportAt("r1", "worker", now, now, "done"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, RetryDelay: time.Second})

	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("want no-orchestrator error for a terminated orchestrator")
	}
	if len(delivery.submissions) != 0 || store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("submissions = %#v, state = %s", delivery.submissions, store.reports[0].DeliveryState)
	}
}

// A stuck report normally interrupts a Chat orchestrator's running turn. An
// exited agent has no turn; interrupting it would fail and release the batch.
func TestCoordinatorDoesNotInterruptExitedChatOrchestrator(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{
		ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityExited},
	}}
	stuck := reportAt("stuck", "worker", now, now, "blocked")
	stuck.State = domain.ReportStuck
	store.add(stuck)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 0 {
		t.Fatalf("interrupts = %v, want none for an exited agent", delivery.interrupts)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want the stuck report delivered", len(delivery.submissions))
	}
}
