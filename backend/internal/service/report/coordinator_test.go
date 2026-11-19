package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCoordinatorFixedFallbackAndSettlementBatch(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("one", "worker", now, now.Add(time.Hour), "first"))
	store.add(reportAt("two", "worker", now.Add(20*time.Minute), now.Add(80*time.Minute), "second"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})

	if delay, err := c.RunDue(context.Background()); err != nil || delay != time.Hour {
		t.Fatalf("before fallback delay = %v, err = %v", delay, err)
	}
	now = now.Add(time.Hour)
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 || !strings.Contains(delivery.submissions[0].message, "first") || !strings.Contains(delivery.submissions[0].message, "second") {
		t.Fatalf("submission = %#v, want one shared batch", delivery.submissions)
	}
	if store.acked != 2 {
		t.Fatalf("acknowledged = %d, want 2", store.acked)
	}

	store.add(reportAt("done-1", "worker", now, now.Add(5*time.Minute), "done"))
	store.add(reportAt("done-2", "worker", now.Add(2*time.Minute), now.Add(7*time.Minute), "later done"))
	now = now.Add(5 * time.Minute)
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 2 || !strings.Contains(delivery.submissions[1].message, "later done") {
		t.Fatalf("settlement submission = %#v", delivery.submissions)
	}
}

func TestCoordinatorRoutesAtAttemptAndLeavesPendingWithoutOrchestrator(t *testing.T) {
	now := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.add(reportAt("need", "worker", now, now, "decision"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, RetryDelay: time.Second})
	if delay, err := c.RunDue(context.Background()); err == nil || delay != time.Second {
		t.Fatalf("no orchestrator delay = %v, err = %v", delay, err)
	}
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("state = %s, want pending", store.reports[0].DeliveryState)
	}
	store.sessions = []domain.SessionRecord{
		{ID: "old", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: true, CreatedAt: now},
		{ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second)},
	}
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "replacement" {
		t.Fatalf("submissions = %#v", delivery.submissions)
	}
}

func TestCoordinatorStuckInterruptRateLimitAndCoalescing(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	first := reportAt("stuck-1", "worker", now, now, "cannot continue")
	first.State = domain.ReportStuck
	second := reportAt("stuck-2", "worker", now.Add(time.Second), now, "cannot continue")
	second.State = domain.ReportStuck
	store.add(first, second)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 1 {
		t.Fatalf("interrupts = %v", delivery.interrupts)
	}
	if !strings.Contains(delivery.submissions[0].message, "repeated 2 times") {
		t.Fatalf("message = %q", delivery.submissions[0].message)
	}
	repeat := reportAt("stuck-3", "worker", now.Add(time.Minute), now, "cannot continue")
	repeat.State = domain.ReportStuck
	store.add(repeat)
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 1 {
		t.Fatalf("rate limited interrupts = %v", delivery.interrupts)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("throttled repeat created an extra wakeup: %#v", delivery.submissions)
	}
}

func TestCoordinatorBusyOrchestratorDoesNotHoldUpOtherProjects(t *testing.T) {
	// Project p's orchestrator composer holds the operator's unsent draft, so its
	// earliest-due batch is refused. It must back off instead of staying first in
	// line and starving project q's reports on every retry.
	now := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "orch-p", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI},
		{ID: "orch-q", ProjectID: "q", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI},
	}
	other := reportAt("q-1", "worker-q", now.Add(time.Second), now, "q finished")
	other.ProjectID = "q"
	store.add(reportAt("p-1", "worker-p", now, now, "p finished"), other)
	delivery := &coordinatorDelivery{submitErrFor: map[domain.SessionID]error{
		"orch-p": fmt.Errorf("send orch-p: %w", ErrOrchestratorBusy),
	}}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})

	delay, err := c.RunDue(context.Background())
	if err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "orch-q" {
		t.Fatalf("submissions = %#v, want q's report delivered past p's busy orchestrator", delivery.submissions)
	}
	if delay != OrchestratorBusyRetry {
		t.Fatalf("next run in %v, want p's backoff %v", delay, OrchestratorBusyRetry)
	}
	for _, rec := range store.reports {
		if rec.ID == "p-1" && (rec.DeliveryState != domain.ReportPending || !rec.AvailableAt.Equal(now.Add(OrchestratorBusyRetry))) {
			t.Fatalf("p's report = %s available %s, want pending at %s", rec.DeliveryState, rec.AvailableAt, now.Add(OrchestratorBusyRetry))
		}
	}

	now = now.Add(OrchestratorBusyRetry)
	delete(delivery.submitErrFor, "orch-p")
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue after backoff: %v", err)
	}
	if len(delivery.submissions) != 2 || delivery.submissions[1].target != "orch-p" {
		t.Fatalf("submissions = %#v, want p's report delivered once its composer cleared", delivery.submissions)
	}
}

func TestCoordinatorNeverInterruptsTerminalOrchestrator(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeTUI, ""} {
		now := time.Date(2026, 10, 28, 2, 10, 0, 0, time.UTC)
		store := newCoordinatorStore(now)
		store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: mode}}
		first := reportAt("stuck-1", "worker", now, now, "cannot continue")
		first.State = domain.ReportStuck
		store.add(first)
		delivery := &coordinatorDelivery{}
		c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
		if _, err := c.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(delivery.interrupts) != 0 || len(delivery.submissions) != 1 {
			t.Fatalf("mode %q: interrupts = %v, submissions = %d", mode, delivery.interrupts, len(delivery.submissions))
		}
		repeat := reportAt("stuck-2", "worker", now.Add(time.Minute), now, "still cannot continue")
		repeat.State = domain.ReportStuck
		store.add(repeat)
		if _, err := c.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(delivery.interrupts) != 0 || len(delivery.submissions) != 2 {
			t.Fatalf("mode %q repeat: interrupts = %v, submissions = %d", mode, delivery.interrupts, len(delivery.submissions))
		}
	}
}

func TestCoordinatorRetriesTerminalStuckReportWithoutInterrupt(t *testing.T) {
	now := time.Date(2026, 10, 28, 2, 10, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}
	stuck := reportAt("stuck-1", "worker", now, now, "cannot continue")
	stuck.State = domain.ReportStuck
	store.add(stuck)
	delivery := &coordinatorDelivery{submitErr: errors.New("composer is not empty")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	for i := 0; i < 3; i++ {
		_, _ = c.RunDue(context.Background())
		now = now.Add(time.Minute)
	}
	delivery.submitErr = nil
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.interrupts) != 0 || len(delivery.submissions) != 1 {
		t.Fatalf("interrupts = %v, submissions = %d", delivery.interrupts, len(delivery.submissions))
	}
}

func TestCoordinatorRefusedSubmitDoesNotBlockOtherProjects(t *testing.T) {
	now := time.Date(2026, 10, 28, 2, 10, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{
		{ID: "refusing", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI},
		{ID: "healthy", ProjectID: "q", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI},
	}
	stuck := reportAt("stuck-1", "worker", now, now, "cannot continue")
	stuck.State = domain.ReportStuck
	done := reportAt("done-1", "other-worker", now.Add(time.Second), now.Add(time.Second), "finished")
	done.ProjectID = "q"
	store.add(stuck, done)
	delivery := &coordinatorDelivery{submitErrFor: map[domain.SessionID]error{"refusing": errors.New("prompt submission was not observed")}}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	for i := 0; i < 5; i++ {
		_, _ = c.RunDue(context.Background())
		now = now.Add(10 * time.Second)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "healthy" {
		t.Fatalf("submissions = %#v, want one delivery to the healthy project", delivery.submissions)
	}
	if len(delivery.interrupts) != 0 {
		t.Fatalf("interrupts = %v", delivery.interrupts)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].LastError != "prompt submission was not observed" {
		t.Fatalf("refused report = %s / %q, want pending with the refusal recorded", store.reports[0].DeliveryState, store.reports[0].LastError)
	}
}

func TestReportSessionLinkUsesDisplayName(t *testing.T) {
	got := reportSessionLink(domain.ReportRecord{
		SessionID: "worker/1", ProjectID: "proj 1", SessionDisplayName: "Backend [worker]",
	})
	want := `[Backend \[worker\]](ao://sessions/proj%201/worker%2F1)`
	if got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
}

func TestCoordinatorAcceptanceAndPiggybackFencing(t *testing.T) {
	now := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("info", "worker", now, now.Add(time.Hour), "context"))
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, NewToken: func() string { return "piggyback-token" }})
	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	combined := batch.AppendToUserMessage("hello")
	if !strings.HasPrefix(combined, "hello\n\n<ao-worker-reports>\n") ||
		!strings.HasSuffix(combined, "\n</ao-worker-reports>") {
		t.Fatalf("piggyback text = %q", combined)
	}
	if !strings.Contains(combined, "[checkpoint] [worker](ao://sessions/p/worker)") {
		t.Fatalf("piggyback link = %q", combined)
	}
	if store.acked != 0 {
		t.Fatal("claimed report acknowledged before acceptance")
	}
	if err := c.AcceptPiggyback(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if store.acked != 1 {
		t.Fatalf("acknowledged = %d", store.acked)
	}
	if count, _ := store.AcknowledgeReportBatch(context.Background(), "p", "stale-token", now); count != 0 {
		t.Fatalf("stale token acknowledged %d reports", count)
	}
}

func TestCoordinatorRetriesStopAtConfiguredLimit(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "progress"))
	delivery := &coordinatorDelivery{submitErr: ErrUnacknowledgedDelivery}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          5 * time.Second,
		MaxDeliveryAttempts: 3,
		Expiry:              time.Hour,
	})

	// Attempt 1
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 1 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("attempt 1: state = %s, attempts = %d", store.reports[0].DeliveryState, store.reports[0].DeliveryAttempts)
	}

	// Advance time past RetryDelay and retry (Attempt 2)
	now = now.Add(5 * time.Second)
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 2 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 2 {
		t.Fatalf("attempt 2: state = %s, attempts = %d", store.reports[0].DeliveryState, store.reports[0].DeliveryAttempts)
	}

	// Advance time past RetryDelay and retry (Attempt 3: limit reached)
	now = now.Add(10 * time.Second)
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 3 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("after 3 attempts: state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}
	if store.reports[0].DeliveryAttempts != 3 {
		t.Fatalf("attempts = %d, want 3", store.reports[0].DeliveryAttempts)
	}
	if store.reports[0].LastError != "prompt submission was not observed" {
		t.Fatalf("last error = %q, want 'prompt submission was not observed'", store.reports[0].LastError)
	}

	// Further calls to RunDue must not attempt delivery again
	now = now.Add(time.Hour)
	delay, err := c.RunDue(context.Background())
	if err != nil || delay != -1 {
		t.Fatalf("delay = %v, err = %v, want -1 and no error (no pending reports left)", delay, err)
	}
	if len(delivery.submissions) != 0 {
		t.Fatalf("submissions = %d, want 0 successful deliveries", len(delivery.submissions))
	}
}

func TestCoordinatorExpiryTerminatesDelivery(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "progress"))
	delivery := &coordinatorDelivery{submitErr: errors.New("service unavailable")}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          time.Minute,
		MaxDeliveryAttempts: 10,
		Expiry:              15 * time.Minute,
	})

	// Attempt 1 at now: fails, within expiry window
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 1 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("state = %s, want pending", store.reports[0].DeliveryState)
	}

	// Advance time past 15 min expiry
	now = now.Add(20 * time.Minute)
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 2 should fail due to expiry")
	}
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("expired report state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}

	// Retries have stopped
	now = now.Add(time.Hour)
	delay, err := c.RunDue(context.Background())
	if err != nil || delay != -1 {
		t.Fatalf("delay = %v, err = %v, want -1", delay, err)
	}
}

func TestCoordinatorNormalDeliveryAcknowledgedUnaffected(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "success"))
	delivery := &coordinatorDelivery{submitErr: errors.New("initial failure")}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          5 * time.Second,
		MaxDeliveryAttempts: 3,
		Expiry:              time.Hour,
	})

	// Attempt 1: fails
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("attempt 1 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("attempt 1: state = %s, attempts = %d", store.reports[0].DeliveryState, store.reports[0].DeliveryAttempts)
	}

	// Recover submit, attempt 2 succeeds
	now = now.Add(5 * time.Second)
	delivery.submitErr = nil
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("attempt 2 failed: %v", err)
	}
	if store.reports[0].DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("state = %s, want acknowledged", store.reports[0].DeliveryState)
	}
	if store.acked != 1 {
		t.Fatalf("acked = %d, want 1", store.acked)
	}
}

func TestCoordinatorBusyOrchestratorDoesNotStopAtAttemptLimitUntilExpiry(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "busy test"))
	delivery := &coordinatorDelivery{submitErr: fmt.Errorf("refused: %w", ErrOrchestratorBusy)}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		MaxDeliveryAttempts: 2,
		Expiry:              10 * time.Minute,
	})

	// Attempt 1
	delay, err := c.RunDue(context.Background())
	if err != nil || delay != OrchestratorBusyRetry {
		t.Fatalf("delay = %v, err = %v, want backoff %v", delay, err, OrchestratorBusyRetry)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("attempt 1: state = %s, attempts = %d", store.reports[0].DeliveryState, store.reports[0].DeliveryAttempts)
	}

	// Attempt 2: at limit, but busy does NOT stop
	now = now.Add(OrchestratorBusyRetry)
	delay, err = c.RunDue(context.Background())
	if err != nil || delay != OrchestratorBusyRetry {
		t.Fatalf("delay = %v, err = %v, want backoff %v", delay, err, OrchestratorBusyRetry)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 2 {
		t.Fatalf("attempt 2: state = %s, want pending", store.reports[0].DeliveryState)
	}

	// Attempt 3: past attempt limit, still keeps waiting
	now = now.Add(OrchestratorBusyRetry)
	delay, err = c.RunDue(context.Background())
	if err != nil || delay != OrchestratorBusyRetry {
		t.Fatalf("delay = %v, err = %v, want backoff %v", delay, err, OrchestratorBusyRetry)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 3 {
		t.Fatalf("attempt 3: state = %s, want pending", store.reports[0].DeliveryState)
	}

	// Advance past expiry window (10 min)
	now = now.Add(15 * time.Minute)
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("state = %s, want %s after expiry", store.reports[0].DeliveryState, domain.ReportFailed)
	}
}

func TestCoordinatorUnavailableBacksOffWithoutExhaustingAttempts(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "unavailable test"))
	delivery := &coordinatorDelivery{submitErr: errors.New("no live chat controller for session")}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          5 * time.Second,
		MaxDeliveryAttempts: 3,
		Expiry:              10 * time.Minute,
	})

	// Attempt 1: backoff factor 1x (5s)
	delay, err := c.RunDue(context.Background())
	if err == nil || delay != 5*time.Second {
		t.Fatalf("attempt 1: delay = %v, want 5s", delay)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("attempt 1: state = %s, attempts = %d", store.reports[0].DeliveryState, store.reports[0].DeliveryAttempts)
	}

	// Attempt 2: backoff factor 2x (10s)
	now = now.Add(5 * time.Second)
	delay, err = c.RunDue(context.Background())
	if err == nil || delay != 5*time.Second { // RunDue returns retryDelay on error
		t.Fatalf("attempt 2 RunDue error: %v", err)
	}
	if store.reports[0].AvailableAt.Sub(now) != 10*time.Second {
		t.Fatalf("attempt 2 deferred availableAt diff = %v, want 10s", store.reports[0].AvailableAt.Sub(now))
	}

	// Attempt 3: backoff factor 4x (20s)
	now = now.Add(10 * time.Second)
	_, _ = c.RunDue(context.Background())
	if store.reports[0].AvailableAt.Sub(now) != 20*time.Second {
		t.Fatalf("attempt 3 deferred availableAt diff = %v, want 20s", store.reports[0].AvailableAt.Sub(now))
	}

	// Attempt 4: attempts = 4 > MaxDeliveryAttempts (3), but does NOT fail because error is transient unavailable
	now = now.Add(20 * time.Second)
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("attempt 4 state = %s, want pending", store.reports[0].DeliveryState)
	}
	if store.reports[0].DeliveryAttempts != 4 {
		t.Fatalf("attempts = %d, want 4", store.reports[0].DeliveryAttempts)
	}

	// Expiry stops it
	now = now.Add(15 * time.Minute)
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("state = %s, want %s after expiry", store.reports[0].DeliveryState, domain.ReportFailed)
	}
}

func TestCoordinatorBusyUnavailableToUnacknowledgedTransition(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "transition test"))
	delivery := &coordinatorDelivery{submitErr: ErrOrchestratorBusy}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          5 * time.Second,
		MaxDeliveryAttempts: 3,
		Expiry:              time.Hour,
	})

	// Attempts 1..3: ErrOrchestratorBusy retries do not count against the unacknowledged attempt budget
	for i := 1; i <= 3; i++ {
		delay, err := c.RunDue(context.Background())
		if err != nil || delay != OrchestratorBusyRetry {
			t.Fatalf("busy attempt %d: delay = %v, err = %v, want delay %v and nil err", i, delay, err, OrchestratorBusyRetry)
		}
		if store.reports[0].DeliveryState != domain.ReportPending {
			t.Fatalf("busy attempt %d: state = %s, want pending", i, store.reports[0].DeliveryState)
		}
		now = now.Add(OrchestratorBusyRetry)
	}
	if store.reports[0].DeliveryAttempts != 3 {
		t.Fatalf("store attempts = %d, want 3", store.reports[0].DeliveryAttempts)
	}

	// Attempt 4: Transient controller-unavailable error also does not count against the budget
	delivery.submitErr = errors.New("no live chat controller for session")
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("unavailable attempt should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("unavailable attempt: state = %s, want pending", store.reports[0].DeliveryState)
	}
	if store.reports[0].DeliveryAttempts != 4 {
		t.Fatalf("store attempts = %d, want 4", store.reports[0].DeliveryAttempts)
	}

	// Now switch to ErrUnacknowledgedDelivery.
	// Even though store.reports[0].DeliveryAttempts is 4 (> MaxDeliveryAttempts 3),
	// the unacknowledged attempt budget is intact and has seen 0 unacknowledged attempts so far.
	delivery.submitErr = ErrUnacknowledgedDelivery

	// Unacknowledged Attempt 1 (Total claim attempt 5): must remain pending
	now = store.reports[0].AvailableAt
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("unacknowledged attempt 1 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("unacknowledged attempt 1: state = %s, want pending", store.reports[0].DeliveryState)
	}
	if store.reports[0].DeliveryAttempts != 5 {
		t.Fatalf("store attempts = %d, want 5", store.reports[0].DeliveryAttempts)
	}

	// Unacknowledged Attempt 2 (Total claim attempt 6): must remain pending
	now = store.reports[0].AvailableAt
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("unacknowledged attempt 2 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("unacknowledged attempt 2: state = %s, want pending", store.reports[0].DeliveryState)
	}
	if store.reports[0].DeliveryAttempts != 6 {
		t.Fatalf("store attempts = %d, want 6", store.reports[0].DeliveryAttempts)
	}

	// Unacknowledged Attempt 3 (Total claim attempt 7: reaches MaxDeliveryAttempts = 3): must transition to failed!
	now = store.reports[0].AvailableAt
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("unacknowledged attempt 3 should fail")
	}
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("after 3 unacknowledged attempts: state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}
	if store.reports[0].DeliveryAttempts != 7 {
		t.Fatalf("store attempts = %d, want 7", store.reports[0].DeliveryAttempts)
	}
	if store.reports[0].LastError != "prompt submission was not observed" {
		t.Fatalf("last error = %q, want 'prompt submission was not observed'", store.reports[0].LastError)
	}
}

func TestCoordinatorSurfacesFailedReportsInNextDelivery(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "fails"))
	delivery := &coordinatorDelivery{submitErr: ErrUnacknowledgedDelivery}
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            delivery,
		Now:                 func() time.Time { return now },
		RetryDelay:          time.Second,
		MaxDeliveryAttempts: 2,
		Expiry:              time.Hour,
	})

	// Fail rep-1 across 2 unacknowledged attempts
	_, _ = c.RunDue(context.Background())
	now = now.Add(time.Second)
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("rep-1 state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}

	// Now add a healthy report rep-2
	delivery.submitErr = nil
	now = now.Add(time.Second)
	store.add(reportAt("rep-2", "worker", now, now, "healthy update"))

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue failed: %v", err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(delivery.submissions))
	}
	sub := delivery.submissions[0].message
	if !strings.Contains(sub, "healthy update") {
		t.Fatalf("submission = %q, want healthy update", sub)
	}
	if !strings.Contains(sub, "Note: 1 report could not be delivered.") {
		t.Fatalf("submission = %q, want undelivered reports note", sub)
	}

	// Next delivery rep-3 should no longer have the note
	now = now.Add(time.Second)
	store.add(reportAt("rep-3", "worker", now, now, "another update"))
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("RunDue failed: %v", err)
	}
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2", len(delivery.submissions))
	}
	sub2 := delivery.submissions[1].message
	if strings.Contains(sub2, "could not be delivered") {
		t.Fatalf("submission 2 = %q, should not contain cleared note", sub2)
	}
}

func TestCoordinatorClearsOnlyIncludedFailureCountOnAcknowledgment(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "fails first"))
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            &coordinatorDelivery{submitErr: ErrUnacknowledgedDelivery},
		Now:                 func() time.Time { return now },
		MaxDeliveryAttempts: 1,
		Expiry:              time.Hour,
	})

	// Fail rep-1
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("rep-1 state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount = %d, want 1", c.UndeliveredCount("p"))
	}

	// Prepare batch A for rep-2 (capturing UndeliveredReports = 1)
	store.add(reportAt("rep-2", "worker", now, now, "piggyback batch"))
	batchA, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batchA.Reports) != 1 {
		t.Fatalf("PreparePiggyback batchA err = %v, reports = %d", err, len(batchA.Reports))
	}
	if batchA.UndeliveredReports != 1 {
		t.Fatalf("batchA.UndeliveredReports = %d, want 1", batchA.UndeliveredReports)
	}

	// Fail another report rep-3 while batch A is in flight
	batchB := PreparedBatch{
		ProjectID: "p",
		Token:     "token-b",
		Reports:   []domain.ReportRecord{reportAt("rep-3", "worker", now, now, "fails later")},
	}
	store.reports = append(store.reports, reportAt("rep-3", "worker", now, now, "fails later"))
	store.reports[len(store.reports)-1].DeliveryState = domain.ReportClaimed
	store.reports[len(store.reports)-1].ClaimToken = "token-b"
	cause := errors.New("another failure")
	if err := c.fail(context.Background(), batchB, cause); !errors.Is(err, cause) {
		t.Fatalf("fail batchB: %v", err)
	}
	if c.UndeliveredCount("p") != 2 {
		t.Fatalf("undeliveredCount after batchB = %d, want 2", c.UndeliveredCount("p"))
	}

	// Accept batch A: must clear only batchA's captured count (1), retaining the 1 failure from batchB
	if err := c.AcceptPiggyback(context.Background(), batchA); err != nil {
		t.Fatalf("AcceptPiggyback batchA: %v", err)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount after accepting batchA = %d, want 1", c.UndeliveredCount("p"))
	}
}

func TestCoordinatorFailDoesNotRecordNoticeWhenStoreFails(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.failReportErr = errors.New("db disk failure")
	onFailedCalled := false
	c := NewCoordinator(CoordinatorDeps{
		Store: store,
		Now:   func() time.Time { return now },
		OnBatchFailed: func(ctx context.Context, batch PreparedBatch, cause error) {
			onFailedCalled = true
		},
	})
	batch := PreparedBatch{
		ProjectID: "p",
		Token:     "tok",
		Reports:   []domain.ReportRecord{reportAt("rep-1", "worker", now, now, "fail")},
	}
	err := c.fail(context.Background(), batch, errors.New("underlying error"))
	if err == nil {
		t.Fatal("expected error from fail when store fails")
	}
	if onFailedCalled {
		t.Fatal("onBatchFailed should not be called when FailReportBatch fails")
	}
	if c.UndeliveredCount("p") != 0 {
		t.Fatalf("undeliveredCount = %d, want 0 when store fails", c.UndeliveredCount("p"))
	}
}

func TestCoordinatorCallsOnBatchFailedWhenBatchFails(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	var capturedBatch PreparedBatch
	var capturedCause error
	called := false
	c := NewCoordinator(CoordinatorDeps{
		Store: store,
		Now:   func() time.Time { return now },
		OnBatchFailed: func(ctx context.Context, batch PreparedBatch, cause error) {
			called = true
			capturedBatch = batch
			capturedCause = cause
		},
	})
	batch := PreparedBatch{
		ProjectID: "p",
		Token:     "tok",
		Reports:   []domain.ReportRecord{reportAt("rep-1", "worker", now, now, "fail")},
	}
	store.reports = append(store.reports, reportAt("rep-1", "worker", now, now, "fail"))
	store.reports[0].DeliveryState = domain.ReportClaimed
	store.reports[0].ClaimToken = "tok"
	underlying := errors.New("terminal failure")
	err := c.fail(context.Background(), batch, underlying)
	if !errors.Is(err, underlying) {
		t.Fatalf("fail err = %v, want %v", err, underlying)
	}
	if !called {
		t.Fatal("expected OnBatchFailed to be called")
	}
	if capturedBatch.Token != "tok" || !errors.Is(capturedCause, underlying) {
		t.Fatalf("captured batch = %v, cause = %v", capturedBatch, capturedCause)
	}
}

func TestCoordinatorOverlappingBatchesFailureNoticeCheckpoint(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("rep-1", "worker", now, now, "fails first"))
	c := NewCoordinator(CoordinatorDeps{
		Store:               store,
		Delivery:            &coordinatorDelivery{submitErr: ErrUnacknowledgedDelivery},
		Now:                 func() time.Time { return now },
		MaxDeliveryAttempts: 1,
		Expiry:              time.Hour,
	})

	// Fail rep-1
	_, _ = c.RunDue(context.Background())
	if store.reports[0].DeliveryState != domain.ReportFailed {
		t.Fatalf("rep-1 state = %s, want %s", store.reports[0].DeliveryState, domain.ReportFailed)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount = %d, want 1", c.UndeliveredCount("p"))
	}

	// Prepare batch A for rep-2 (capturing UndeliveredReports = 1, FailureCheckpoint = 1)
	store.add(reportAt("rep-2", "worker", now, now, "piggyback batch A"))
	batchA, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batchA.Reports) != 1 {
		t.Fatalf("PreparePiggyback batchA err = %v, reports = %d", err, len(batchA.Reports))
	}
	if batchA.UndeliveredReports != 1 || batchA.FailureCheckpoint != 1 {
		t.Fatalf("batchA undelivered = %d, checkpoint = %d, want 1, 1", batchA.UndeliveredReports, batchA.FailureCheckpoint)
	}

	// Prepare batch B for rep-3 while batch A is in flight (also capturing UndeliveredReports = 1, FailureCheckpoint = 1)
	store.add(reportAt("rep-3", "worker", now, now, "piggyback batch B"))
	batchB, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batchB.Reports) != 1 {
		t.Fatalf("PreparePiggyback batchB err = %v, reports = %d", err, len(batchB.Reports))
	}
	if batchB.UndeliveredReports != 1 || batchB.FailureCheckpoint != 1 {
		t.Fatalf("batchB undelivered = %d, checkpoint = %d, want 1, 1", batchB.UndeliveredReports, batchB.FailureCheckpoint)
	}

	// Deliver and accept batch B
	if err := c.AcceptPiggyback(context.Background(), batchB); err != nil {
		t.Fatalf("AcceptPiggyback batchB: %v", err)
	}
	if c.UndeliveredCount("p") != 0 {
		t.Fatalf("undeliveredCount after batchB = %d, want 0", c.UndeliveredCount("p"))
	}

	// Intervening failure: rep-4 fails while batch A is still in flight
	batchFail := PreparedBatch{
		ProjectID: "p",
		Token:     "tok-fail",
		Reports:   []domain.ReportRecord{reportAt("rep-4", "worker", now, now, "intervening failure")},
	}
	store.reports = append(store.reports, reportAt("rep-4", "worker", now, now, "intervening failure"))
	store.reports[len(store.reports)-1].DeliveryState = domain.ReportClaimed
	store.reports[len(store.reports)-1].ClaimToken = "tok-fail"
	cause := errors.New("intervening error")
	if err := c.fail(context.Background(), batchFail, cause); !errors.Is(err, cause) {
		t.Fatalf("fail batchFail: %v", err)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount after intervening failure = %d, want 1", c.UndeliveredCount("p"))
	}

	// Zero-row acknowledgment test: accepting a stale batch affecting zero rows must not consume notice
	staleBatch := PreparedBatch{
		ProjectID:          "p",
		Token:              "stale-token-not-in-store",
		Reports:            []domain.ReportRecord{reportAt("rep-stale", "worker", now, now, "stale")},
		UndeliveredReports: 1,
		FailureCheckpoint:  2,
	}
	if err := c.AcceptPiggyback(context.Background(), staleBatch); err != nil {
		t.Fatalf("AcceptPiggyback staleBatch: %v", err)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount after zero-row staleBatch = %d, want 1", c.UndeliveredCount("p"))
	}

	// Accept batch A: batchA carries FailureCheckpoint = 1, but failureAckedSeq is already 1.
	// Accepting A must not subtract its stale snapshot and must preserve rep-4's failure!
	if err := c.AcceptPiggyback(context.Background(), batchA); err != nil {
		t.Fatalf("AcceptPiggyback batchA: %v", err)
	}
	if c.UndeliveredCount("p") != 1 {
		t.Fatalf("undeliveredCount after accepting batchA = %d, want 1 (must not erase intervening failure)", c.UndeliveredCount("p"))
	}

	// Verify next prepared batch includes the intervening failure notice
	store.add(reportAt("rep-5", "worker", now, now, "batch C"))
	batchC, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batchC.Reports) != 1 {
		t.Fatalf("PreparePiggyback batchC err = %v, reports = %d", err, len(batchC.Reports))
	}
	if batchC.UndeliveredReports != 1 || batchC.FailureCheckpoint != 2 {
		t.Fatalf("batchC undelivered = %d, checkpoint = %d, want 1, 2", batchC.UndeliveredReports, batchC.FailureCheckpoint)
	}

	// Deliver and accept batch C
	if err := c.AcceptPiggyback(context.Background(), batchC); err != nil {
		t.Fatalf("AcceptPiggyback batchC: %v", err)
	}
	if c.UndeliveredCount("p") != 0 {
		t.Fatalf("undeliveredCount after batchC = %d, want 0", c.UndeliveredCount("p"))
	}
}

type coordinatorStore struct {
	unacknowledgedAttempts map[string]int
	mu                     sync.Mutex
	now                    time.Time
	reports                []domain.ReportRecord
	sessions               []domain.SessionRecord
	interrupts             map[domain.SessionID]time.Time
	acked                  int
	failReportErr          error
}

func newCoordinatorStore(now time.Time) *coordinatorStore {
	return &coordinatorStore{unacknowledgedAttempts: map[string]int{}, now: now, interrupts: map[domain.SessionID]time.Time{}}
}

func reportAt(id string, worker domain.SessionID, created, available time.Time, note string) domain.ReportRecord {
	return domain.ReportRecord{ID: id, SessionID: worker, ProjectID: "p", State: domain.ReportCheckpoint, Note: note, CreatedAt: created, AvailableAt: available, DeliveryState: domain.ReportPending, RepeatCount: 1}
}

func (s *coordinatorStore) add(records ...domain.ReportRecord) {
	s.reports = append(s.reports, records...)
}
func (s *coordinatorStore) setSessions(sessions ...domain.SessionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = sessions
}
func (s *coordinatorStore) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var recs []domain.SessionRecord
	for _, rec := range s.sessions {
		if rec.ProjectID == project {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}
func (s *coordinatorStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.sessions {
		if rec.ID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}
func (s *coordinatorStore) ListPendingReportSchedule(context.Context, int64) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []domain.ReportRecord
	for _, rec := range s.reports {
		if rec.DeliveryState == domain.ReportPending {
			pending = append(pending, rec)
		}
	}
	sortReports(pending)
	if len(pending) > 1 {
		pending = pending[:1]
	}
	return pending, nil
}
func (s *coordinatorStore) ListPendingReportsOfDoneProjects(context.Context) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	done := map[domain.ProjectID]bool{}
	for _, rec := range s.reports {
		if rec.DeliveryState == domain.ReportPending && rec.State == domain.ReportDone {
			done[rec.ProjectID] = true
		}
	}
	var pending []domain.ReportRecord
	for _, rec := range s.reports {
		if rec.DeliveryState == domain.ReportPending && done[rec.ProjectID] {
			pending = append(pending, rec)
		}
	}
	sortReports(pending)
	return pending, nil
}
func (s *coordinatorStore) ClaimPendingReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Like the store: the project's earliest scheduled report names the batch,
	// and unbatched reports join it only when that report is unbatched too.
	var scheduled []domain.ReportRecord
	for _, rec := range s.reports {
		if rec.ProjectID == project && rec.DeliveryState == domain.ReportPending {
			scheduled = append(scheduled, rec)
		}
	}
	if len(scheduled) == 0 {
		return nil, nil
	}
	sortReports(scheduled)
	batchID := scheduled[0].DeliveryBatchID
	if batchID == "" {
		batchID = "report-batch:" + scheduled[0].ID
	}
	var claimed []domain.ReportRecord
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending {
			if s.reports[i].DeliveryBatchID != batchID && (s.reports[i].DeliveryBatchID != "" || scheduled[0].DeliveryBatchID != "") {
				continue
			}
			s.reports[i].DeliveryBatchID = batchID
			s.reports[i].DeliveryAttempts++
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].ClaimedAt = domain.ReportClaimed, token, at
			claimed = append(claimed, s.reports[i])
		}
	}
	sortReports(claimed)
	return claimed, nil
}
func (s *coordinatorStore) AcknowledgeReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].AcknowledgedAt = domain.ReportAcknowledged, at
			count++
		}
	}
	s.acked += count
	return count, nil
}
func (s *coordinatorStore) ReleaseReportBatch(_ context.Context, project domain.ProjectID, token string, _ time.Time, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken = domain.ReportPending, ""
			s.reports[i].LastError = reason
			count++
		}
	}
	return count, nil
}
func (s *coordinatorStore) FailReportBatch(_ context.Context, project domain.ProjectID, token string, _ time.Time, reason string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReportErr != nil {
		return 0, s.failReportErr
	}
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken = domain.ReportFailed, ""
			s.reports[i].LastError = reason
			count++
		}
	}
	return count, nil
}
func (s *coordinatorStore) RetryUnacknowledgedReportBatch(ctx context.Context, project domain.ProjectID, token string, at time.Time, limit int, reason string) (int, error) {
	s.mu.Lock()
	exceeded := false
	for _, rec := range s.reports {
		if rec.ProjectID == project && rec.DeliveryState == domain.ReportClaimed && rec.ClaimToken == token {
			s.unacknowledgedAttempts[rec.ID]++
			exceeded = exceeded || s.unacknowledgedAttempts[rec.ID] >= limit
		}
	}
	s.mu.Unlock()
	if exceeded {
		return s.FailReportBatch(ctx, project, token, at, reason)
	}
	_, err := s.DeferReportBatch(ctx, project, token, at, reason)
	return 0, err
}

func (s *coordinatorStore) DeferReportBatch(ctx context.Context, project domain.ProjectID, token string, at time.Time, reason string) (int, error) {
	count, err := s.ReleaseReportBatch(ctx, project, token, at, reason)
	if err != nil {
		return count, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending && s.reports[i].DeliveryBatchID != "" && s.reports[i].AvailableAt.Before(at) {
			s.reports[i].AvailableAt = at
		}
	}
	return count, nil
}
func (s *coordinatorStore) AcquireReportInterrupt(_ context.Context, worker domain.SessionID, at time.Time, window time.Duration) (bool, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.interrupts[worker]
	if ok && last.After(at.Add(-window)) {
		return false, last.Add(window), nil
	}
	s.interrupts[worker] = at
	return true, at, nil
}

func sortReports(records []domain.ReportRecord) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0 && (records[j].AvailableAt.Before(records[j-1].AvailableAt) || (records[j].AvailableAt.Equal(records[j-1].AvailableAt) && records[j].ID < records[j-1].ID)); j-- {
			records[j], records[j-1] = records[j-1], records[j]
		}
	}
}

type coordinatorSubmission struct {
	target       domain.SessionID
	message, key string
}
type coordinatorDelivery struct {
	mu           sync.Mutex
	submissions  []coordinatorSubmission
	interrupts   []domain.SessionID
	submitErr    error
	submitErrFor map[domain.SessionID]error
	attempts     int
}

func (d *coordinatorDelivery) Submit(_ context.Context, target domain.SessionID, message, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attempts++
	if d.submitErr != nil {
		return d.submitErr
	}
	if err := d.submitErrFor[target]; err != nil {
		return err
	}
	d.submissions = append(d.submissions, coordinatorSubmission{target, message, key})
	return nil
}
func (d *coordinatorDelivery) submitted() []coordinatorSubmission {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]coordinatorSubmission(nil), d.submissions...)
}
func (d *coordinatorDelivery) Interrupt(_ context.Context, target domain.SessionID) error {
	d.interrupts = append(d.interrupts, target)
	return nil
}
