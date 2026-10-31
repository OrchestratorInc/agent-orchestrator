package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func doneAt(id string, worker domain.SessionID, created time.Time, note string) domain.ReportRecord {
	rec := reportAt(id, worker, created, created.Add(domain.ReportSettlementWindow), note)
	rec.State, rec.SettlementDeadline = domain.ReportDone, rec.AvailableAt
	return rec
}

func earlyOrchestrator() domain.SessionRecord {
	return domain.SessionRecord{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}
}

func earlyWorker(id domain.SessionID, state domain.ActivityState, at time.Time) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "p", Kind: domain.KindWorker, Activity: domain.Activity{State: state, LastActivityAt: at}}
}

type earlyFlushHarness struct {
	now      time.Time
	store    *coordinatorStore
	delivery *coordinatorDelivery
	c        *Coordinator
}

func newEarlyFlushHarness(now time.Time) *earlyFlushHarness {
	h := &earlyFlushHarness{now: now, store: newCoordinatorStore(now), delivery: &coordinatorDelivery{}}
	h.c = NewCoordinator(CoordinatorDeps{Store: h.store, Delivery: h.delivery, Now: func() time.Time { return h.now }, RetryDelay: time.Second})
	return h
}

func (h *earlyFlushHarness) run(t *testing.T) time.Duration {
	t.Helper()
	delay, err := h.c.RunDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return delay
}

func TestCoordinatorEarlyFlushSingleWorkerAfterDebounce(t *testing.T) {
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	h := newEarlyFlushHarness(start)
	h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start.Add(-time.Minute)))
	h.store.add(doneAt("done-a", "a", start, "finished"))

	if delay := h.run(t); delay != domain.ReportEarlyFlushDebounce || len(h.delivery.submitted()) != 0 {
		t.Fatalf("debounce delay = %v, submissions = %d", delay, len(h.delivery.submitted()))
	}
	h.now = start.Add(domain.ReportEarlyFlushDebounce)
	if delay := h.run(t); delay != -1 {
		t.Fatalf("delay after flush = %v, want idle", delay)
	}
	got := h.delivery.submitted()
	if len(got) != 1 || got[0].target != "orch" || !strings.Contains(got[0].message, "finished") {
		t.Fatalf("submissions = %#v", got)
	}
}

func TestCoordinatorEarlyFlushWaitsForWorkingWorker(t *testing.T) {
	start := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		finish func(h *earlyFlushHarness, at time.Time)
	}{
		{"other reports done", func(h *earlyFlushHarness, at time.Time) {
			h.store.add(doneAt("done-b", "b", at, "b finished"))
		}},
		{"other goes idle", func(h *earlyFlushHarness, at time.Time) {
			h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start), earlyWorker("b", domain.ActivityIdle, at))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newEarlyFlushHarness(start)
			h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start), earlyWorker("b", domain.ActivityActive, start.Add(-time.Hour)))
			h.store.add(doneAt("done-a", "a", start, "a finished"))

			h.now = start.Add(time.Minute)
			if delay := h.run(t); delay != domain.ReportSettlementWindow-time.Minute || len(h.delivery.submitted()) != 0 {
				t.Fatalf("busy worker delay = %v, submissions = %d", delay, len(h.delivery.submitted()))
			}
			finished := start.Add(2 * time.Minute)
			tc.finish(h, finished)
			h.now = finished
			if delay := h.run(t); delay != domain.ReportEarlyFlushDebounce || len(h.delivery.submitted()) != 0 {
				t.Fatalf("debounce delay = %v, submissions = %d", delay, len(h.delivery.submitted()))
			}
			h.now = finished.Add(domain.ReportEarlyFlushDebounce)
			h.run(t)
			got := h.delivery.submitted()
			if len(got) != 1 || !strings.Contains(got[0].message, "a finished") {
				t.Fatalf("submissions = %#v, want one early batch", got)
			}
			if strings.Contains(tc.name, "done") && !strings.Contains(got[0].message, "b finished") {
				t.Fatalf("batch = %q, want both done reports", got[0].message)
			}
			h.run(t)
			if len(h.delivery.submitted()) != 1 {
				t.Fatalf("batch delivered more than once: %#v", h.delivery.submitted())
			}
		})
	}
}

func TestCoordinatorEarlyFlushKeepsSettlementUpperBound(t *testing.T) {
	start := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	h := newEarlyFlushHarness(start)
	h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start), earlyWorker("b", domain.ActivityActive, start))
	h.store.add(doneAt("done-a", "a", start, "a finished"))

	h.now = start.Add(domain.ReportSettlementWindow - time.Second)
	if delay := h.run(t); delay != time.Second || len(h.delivery.submitted()) != 0 {
		t.Fatalf("delay = %v, submissions = %d", delay, len(h.delivery.submitted()))
	}
	h.now = start.Add(domain.ReportSettlementWindow)
	h.run(t)
	if got := h.delivery.submitted(); len(got) != 1 || !strings.Contains(got[0].message, "a finished") {
		t.Fatalf("submissions at deadline = %#v", got)
	}
}

func TestCoordinatorEarlyFlushCarriesPendingCheckpoints(t *testing.T) {
	start := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	h := newEarlyFlushHarness(start)
	h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start), earlyWorker("c", domain.ActivityIdle, start))
	h.store.add(reportAt("checkpoint-c", "c", start.Add(-10*time.Minute), start.Add(50*time.Minute), "c progress"))
	h.store.add(doneAt("done-a", "a", start, "a finished"))

	h.now = start.Add(domain.ReportEarlyFlushDebounce)
	h.run(t)
	got := h.delivery.submitted()
	if len(got) != 1 || !strings.Contains(got[0].message, "a finished") || !strings.Contains(got[0].message, "c progress") {
		t.Fatalf("submissions = %#v, want done and checkpoint in one batch", got)
	}
	if h.store.acked != 2 {
		t.Fatalf("acknowledged = %d, want 2", h.store.acked)
	}
}

func TestCoordinatorEarlyFlushIgnoresStoppedAndNonWorkerSessions(t *testing.T) {
	start := time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		other domain.SessionRecord
	}{
		{"terminated worker", func() domain.SessionRecord {
			rec := earlyWorker("b", domain.ActivityActive, start.Add(-time.Hour))
			rec.IsTerminated = true
			return rec
		}()},
		{"exited worker", earlyWorker("b", domain.ActivityExited, start.Add(-time.Hour))},
		{"waiting worker", earlyWorker("b", domain.ActivityWaitingInput, start.Add(-time.Hour))},
		{"blocked worker", earlyWorker("b", domain.ActivityBlocked, start.Add(-time.Hour))},
		{"task preparation", func() domain.SessionRecord {
			rec := earlyWorker("b", domain.ActivityActive, start.Add(-time.Hour))
			rec.IsTaskPreparation = true
			return rec
		}()},
		{"working orchestrator", domain.SessionRecord{ID: "old-orch", ProjectID: "p", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: start.Add(-time.Hour)}, CreatedAt: start.Add(-time.Hour)}},
		{"active done reporter", earlyWorker("a", domain.ActivityActive, start.Add(-time.Hour))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newEarlyFlushHarness(start)
			orch := earlyOrchestrator()
			orch.Activity.State, orch.CreatedAt = domain.ActivityActive, start
			sessions := []domain.SessionRecord{orch, tc.other}
			if tc.other.ID != "a" {
				sessions = append(sessions, earlyWorker("a", domain.ActivityIdle, start.Add(-time.Hour)))
			}
			h.store.setSessions(sessions...)
			h.store.add(doneAt("done-a", "a", start, "a finished"))
			h.now = start.Add(domain.ReportEarlyFlushDebounce)
			h.run(t)
			if got := h.delivery.submitted(); len(got) != 1 || got[0].target != "orch" {
				t.Fatalf("submissions = %#v, want early flush to orch", got)
			}
		})
	}
}

func TestCoordinatorEarlyFlushWithoutOrchestratorKeepsDeadline(t *testing.T) {
	start := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	h := newEarlyFlushHarness(start)
	h.store.setSessions(earlyWorker("a", domain.ActivityIdle, start))
	h.store.add(doneAt("done-a", "a", start, "a finished"))

	h.now = start.Add(time.Minute)
	if delay := h.run(t); delay != domain.ReportSettlementWindow-time.Minute {
		t.Fatalf("delay = %v, want remaining settlement window without a retry loop", delay)
	}
	if h.store.reports[0].DeliveryState != domain.ReportPending {
		t.Fatalf("state = %s, want pending", h.store.reports[0].DeliveryState)
	}
	h.now = start.Add(domain.ReportSettlementWindow)
	if delay, err := h.c.RunDue(context.Background()); err == nil || delay != time.Second {
		t.Fatalf("due without orchestrator delay = %v, err = %v; want today's retry", delay, err)
	}
}

func TestCoordinatorSessionActivityChangeWakes(t *testing.T) {
	c := NewCoordinator(CoordinatorDeps{})
	c.SessionActivityChanged(domain.ActivityActive, false)
	select {
	case <-c.wake:
		t.Fatal("active session woke the coordinator")
	default:
	}
	for _, change := range []struct {
		state      domain.ActivityState
		terminated bool
	}{
		{domain.ActivityIdle, false},
		{domain.ActivityWaitingInput, false},
		{domain.ActivityExited, false},
		{domain.ActivityActive, true},
	} {
		c.SessionActivityChanged(change.state, change.terminated)
		select {
		case <-c.wake:
		default:
			t.Fatalf("%s (terminated=%v) did not wake the coordinator", change.state, change.terminated)
		}
	}
}

func TestCoordinatorActivityWakeFlushesBeforeDeadline(t *testing.T) {
	start := time.Now().UTC()
	store := newCoordinatorStore(start)
	store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start.Add(-time.Hour)), earlyWorker("b", domain.ActivityActive, start.Add(-time.Hour)))
	store.add(doneAt("done-a", "a", start.Add(-time.Minute), "a finished"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery})
	ctx, cancel := context.WithCancel(context.Background())
	done := c.Start(ctx)
	defer func() { cancel(); <-done }()

	time.Sleep(50 * time.Millisecond)
	if len(delivery.submitted()) != 0 {
		t.Fatal("flushed while another worker was working")
	}
	store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start.Add(-time.Hour)), earlyWorker("b", domain.ActivityIdle, start.Add(-time.Hour)))
	c.SessionActivityChanged(domain.ActivityIdle, false)
	deadline := time.Now().Add(5 * time.Second)
	for len(delivery.submitted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("activity change did not flush the pending done batch")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCoordinatorEarlyFlushBacksOffBusyOrchestrator(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	h := newEarlyFlushHarness(start)
	h.delivery.submitErrFor = map[domain.SessionID]error{"orch": ErrOrchestratorBusy}
	h.store.setSessions(earlyOrchestrator(), earlyWorker("a", domain.ActivityIdle, start.Add(-time.Hour)))
	h.store.add(doneAt("done-a", "a", start, "a finished"))

	h.now = start.Add(domain.ReportEarlyFlushDebounce)
	if delay := h.run(t); delay != OrchestratorBusyRetry || h.delivery.attempts != 1 {
		t.Fatalf("busy delay = %v, attempts = %d; want one attempt and the busy backoff", delay, h.delivery.attempts)
	}
	h.c.SessionActivityChanged(domain.ActivityIdle, false)
	h.now = h.now.Add(time.Second)
	if delay := h.run(t); delay != OrchestratorBusyRetry-time.Second || h.delivery.attempts != 1 {
		t.Fatalf("wake inside backoff delay = %v, attempts = %d; want no retry", delay, h.delivery.attempts)
	}
	h.delivery.submitErrFor = nil
	h.now = start.Add(domain.ReportEarlyFlushDebounce + OrchestratorBusyRetry)
	h.run(t)
	if got := h.delivery.submitted(); len(got) != 1 || h.delivery.attempts != 2 {
		t.Fatalf("after backoff submissions = %#v, attempts = %d", got, h.delivery.attempts)
	}

	// A later done report flushes after the plain debounce; the hold is gone.
	h.store.add(doneAt("done-a2", "a", h.now, "a again"))
	if delay := h.run(t); delay != domain.ReportEarlyFlushDebounce {
		t.Fatalf("next batch delay = %v, want plain debounce", delay)
	}
}
