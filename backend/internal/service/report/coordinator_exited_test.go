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
