package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 自动投递和用户发言夹带报告都只能领取当前编排器自己的任务。
func TestIndependentOrchestratorReportsStayWithOwner(t *testing.T) {
	now := time.Now().UTC()
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "primary", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
		{ID: "a", ProjectID: "p", Kind: domain.KindOrchestrator, IndependentOrchestrator: true, Mode: domain.SessionModeChat},
		{ID: "b", ProjectID: "p", Kind: domain.KindOrchestrator, IndependentOrchestrator: true, Mode: domain.SessionModeChat},
		{ID: "worker-a", ProjectID: "p", OrchestratorSessionID: "a"},
		{ID: "worker-b", ProjectID: "p", OrchestratorSessionID: "b"},
	}
	store.add(reportAt("ra", "worker-a", now, now, "only-a"), reportAt("rb", "worker-b", now, now, "only-b"), reportAt("rp", "legacy", now, now, "only-primary"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	batch, err := c.PreparePiggyback(context.Background(), "a")
	if err != nil || len(batch.Reports) != 1 || batch.Reports[0].ID != "ra" {
		t.Fatalf("piggyback = %+v: %v", batch, err)
	}
	if err := c.AcceptPiggyback(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 2 {
		t.Fatalf("deliveries = %+v", delivery.submissions)
	}
	for _, submission := range delivery.submissions {
		if !strings.Contains(submission.message, "only-"+string(submission.target)) || strings.Contains(submission.message, "only-a") {
			t.Fatalf("wrong target: %+v", submission)
		}
	}
	store.sessions[1].IsTerminated = true
	store.add(reportAt("late-a", "worker-a", now, now, "wait-for-a"), reportAt("late-b", "worker-b", now, now, "only-b"))
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 3 || delivery.submissions[2].target != "b" {
		t.Fatalf("unavailable owner blocked sibling: %+v", delivery.submissions)
	}
}
