package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var errSemanticUnsupported = errors.New("session: semantic message acceptance unsupported")

func TestCoordinatorFailureDoesNotStarveNewerReportBatch(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{
		ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
	}}

	old := reportAt("old", "worker-a", now.Add(-2*time.Minute), now.Add(-2*time.Minute), "old undeliverable")
	old.DeliveryBatchID = "report-batch:old"
	newer := reportAt("newer", "worker-b", now.Add(-time.Minute), now.Add(-time.Minute), "newer done")
	newer.DeliveryBatchID = "report-batch:newer"
	store.add(old, newer)

	delivery := &coordinatorDelivery{submitErrByKey: map[string]error{"report-batch:old": errSemanticUnsupported}}
	c := NewCoordinator(CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now:        func() time.Time { return now },
		NewToken:   func() string { return "claim-token" },
		RetryDelay: 5 * time.Second,
	})

	delay, err := c.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue err = %v, want nil after newer batch succeeds", err)
	}
	if delay != 5*time.Second {
		t.Fatalf("RunDue delay = %v, want backoff until deferred older batch", delay)
	}
	if got := store.reportAttempts("old"); got != 1 {
		t.Fatalf("old attempts = %d, want 1", got)
	}
	if got := store.reportAttempts("newer"); got != 1 {
		t.Fatalf("newer attempts = %d, want 1; keys=%v", got, delivery.submitKeys)
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d, want newer acknowledged", store.acked)
	}
	if got := store.reportLastError("old"); got != errSemanticUnsupported.Error() {
		t.Fatalf("old last_error = %q", got)
	}
	if !store.reportAvailableAt("old").Equal(now.Add(5 * time.Second)) {
		t.Fatalf("old available_at = %s, want deferred", store.reportAvailableAt("old"))
	}
}

func TestCoordinatorFailureDoesNotStarveOtherProjectBatch(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "orch-a", ProjectID: "pa", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
		{ID: "orch-b", ProjectID: "pb", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
	}
	old := reportAt("old-a", "wa", now.Add(-2*time.Minute), now.Add(-2*time.Minute), "fail a")
	old.ProjectID = "pa"
	old.DeliveryBatchID = "report-batch:old-a"
	other := reportAt("other-b", "wb", now.Add(-time.Minute), now.Add(-time.Minute), "ok b")
	other.ProjectID = "pb"
	other.DeliveryBatchID = "report-batch:other-b"
	store.add(old, other)

	delivery := &coordinatorDelivery{submitErrByKey: map[string]error{"report-batch:old-a": errSemanticUnsupported}}
	c := NewCoordinator(CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now: func() time.Time { return now }, NewToken: func() string { return "tok" }, RetryDelay: time.Second,
	})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue err = %v", err)
	}
	if store.reportAttempts("old-a") != 1 || store.reportAttempts("other-b") != 1 {
		t.Fatalf("attempts old-a=%d other-b=%d keys=%v", store.reportAttempts("old-a"), store.reportAttempts("other-b"), delivery.submitKeys)
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d", store.acked)
	}
}

func TestCoordinatorNoOrchestratorDoesNotStarveOtherProject(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "orch-b", ProjectID: "pb", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat},
	}
	blocked := reportAt("blocked", "wa", now.Add(-2*time.Minute), now.Add(-2*time.Minute), "no orch")
	blocked.ProjectID = "pa"
	blocked.DeliveryBatchID = "report-batch:blocked"
	ok := reportAt("ok", "wb", now.Add(-time.Minute), now.Add(-time.Minute), "deliver me")
	ok.ProjectID = "pb"
	ok.DeliveryBatchID = "report-batch:ok"
	store.add(blocked, ok)

	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now: func() time.Time { return now }, NewToken: func() string { return "tok" }, RetryDelay: 5 * time.Second,
	})
	delay, err := c.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue err = %v, want nil after other project succeeds", err)
	}
	if delay != 5*time.Second {
		t.Fatalf("delay = %v, want deferred blocked project", delay)
	}
	if store.reportAttempts("blocked") != 1 || store.reportAttempts("ok") != 1 {
		t.Fatalf("attempts blocked=%d ok=%d", store.reportAttempts("blocked"), store.reportAttempts("ok"))
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d, want pb batch", store.acked)
	}
}

func TestCoordinatorTransientSubmitAlsoBacksOffAndUnblocksNewer(t *testing.T) {
	// Claim API always takes the project's oldest pending batch, so same-project
	// isolation requires backing off even transient Submit errors (not in-cycle skip).
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{
		ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
	}}
	old := reportAt("old", "wa", now.Add(-2*time.Minute), now.Add(-2*time.Minute), "flaky")
	old.DeliveryBatchID = "report-batch:old"
	newer := reportAt("newer", "wb", now.Add(-time.Minute), now.Add(-time.Minute), "ok")
	newer.DeliveryBatchID = "report-batch:newer"
	store.add(old, newer)

	delivery := &coordinatorDelivery{submitErrByKey: map[string]error{"report-batch:old": errors.New("temporary network blip")}}
	c := NewCoordinator(CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now: func() time.Time { return now }, NewToken: func() string { return "tok" }, RetryDelay: 5 * time.Second,
	})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue err = %v", err)
	}
	if store.reportAttempts("old") != 1 || store.reportAttempts("newer") != 1 {
		t.Fatalf("attempts old=%d newer=%d keys=%v", store.reportAttempts("old"), store.reportAttempts("newer"), delivery.submitKeys)
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d", store.acked)
	}
	if !store.reportAvailableAt("old").Equal(now.Add(5 * time.Second)) {
		t.Fatalf("old available_at = %s, want deferred by retryDelay", store.reportAvailableAt("old"))
	}
}

func (s *coordinatorStore) reportAttempts(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.reports {
		if rec.ID == id {
			return rec.DeliveryAttempts
		}
	}
	return 0
}

func (s *coordinatorStore) reportLastError(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.reports {
		if rec.ID == id {
			return rec.LastError
		}
	}
	return ""
}

func (s *coordinatorStore) reportAvailableAt(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.reports {
		if rec.ID == id {
			return rec.AvailableAt
		}
	}
	return time.Time{}
}
