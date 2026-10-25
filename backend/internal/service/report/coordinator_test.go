package report

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	// Without an orchestrator the due batch reschedules at the backoff instead
	// of erroring at the head forever, and keeps its visible retry state.
	if delay, err := c.RunDue(context.Background()); err != nil || delay != ReportOrchestratorBackoff {
		t.Fatalf("no orchestrator delay = %v, err = %v", delay, err)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].LastError == "" || store.reports[0].DeliveryAttempts != 1 {
		t.Fatalf("deferred state = %+v", store.reports[0])
	}
	store.sessions = []domain.SessionRecord{
		{ID: "old", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: true, CreatedAt: now},
		{ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second)},
	}
	now = store.reports[0].AvailableAt
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

// TestCoordinatorPiggybackLostClaimDoesNotReplayBatch reproduces the issue #101
// replay cycle: a long turn's claim is stolen past the lease window (daemon
// restart requeue), the concurrent RunDue path redelivers the batch, and the
// chat turn's late acknowledgment must not report success. The acknowledged
// batch must not be injected into a following user message again.
func TestCoordinatorPiggybackLostClaimDoesNotReplayBatch(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("info", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	tokens := []string{"chat-token", "due-token"}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return tokens[0] }})

	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 || batch.Token != "chat-token" {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	injected := batch.AppendToUserMessage("hello")
	if !strings.Contains(injected, "context") {
		t.Fatalf("expected the batch injected into the turn: %q", injected)
	}

	// The lease window lapses: a daemon restart requeues the stolen claim.
	store.requeue()

	// The concurrent RunDue path claims with a fresh token and delivers once.
	tokens[0] = "due-token"
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("deliveries = %d, want exactly one", len(delivery.submissions))
	}

	// The acknowledged batch cannot be injected into a following turn.
	next, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(next.Reports) != 0 {
		t.Fatalf("replayed prepare = %#v, err = %v", next, err)
	}
	if again := next.AppendToUserMessage("next turn"); again != "next turn" {
		t.Fatalf("replayed injection = %q", again)
	}

	// The late acknowledgment of the stolen claim must not be a silent success.
	if err := c.AcceptPiggyback(context.Background(), batch); err == nil {
		t.Fatal("lost-claim acknowledgment succeeded silently")
	}
	if c.LostAcknowledgements() != 1 {
		t.Fatalf("lost acknowledgements = %d, want 1", c.LostAcknowledgements())
	}
	if c.Redeliveries() != 1 {
		t.Fatalf("redeliveries = %d, want 1", c.Redeliveries())
	}
}

// TestCoordinatorBusyClaimBlocksRunDue pins the no-crash path: while a piggyback
// turn holds its claim, a concurrent RunDue delivers nothing, and the healthy
// acknowledgment still succeeds.
func TestCoordinatorBusyClaimBlocksRunDue(t *testing.T) {
	now := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("info", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "chat-token" }})

	batch, err := c.PreparePiggyback(context.Background(), "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	delay, err := c.RunDue(context.Background())
	if err != nil || delay >= 0 {
		t.Fatalf("RunDue while claimed should find nothing due: delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 0 || c.Redeliveries() != 0 {
		t.Fatalf("claimed batch redelivered: %v", delivery.submissions)
	}
	if err := c.AcceptPiggyback(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if remaining, err := c.RunDue(context.Background()); err != nil || remaining >= 0 {
		t.Fatalf("RunDue after ack should stay idle: delay = %v, err = %v", remaining, err)
	}
}

// TestCoordinatorDeferredBatchReclaimsAfterExpiry pins the literal issue #121
// lane that already works: a stuck batch deferred inside the interrupt window
// is re-claimed once the clock shifts past its availability and delivers.
func TestCoordinatorDeferredBatchReclaimsAfterExpiry(t *testing.T) {
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	stuck := reportAt("stuck", "worker", now, now, "cannot continue")
	stuck.State = domain.ReportStuck
	store.add(stuck)
	// The interrupt budget is spent, so the first pass defers inside the window.
	store.interrupts["worker"] = now
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.reports[0].DeliveryState != domain.ReportPending || !store.reports[0].AvailableAt.After(now) {
		t.Fatalf("deferred batch = %+v", store.reports[0])
	}

	// The clock shifts past the deferral while the old orchestrator is gone;
	// the replacement session is running by the time the deferral expires.
	now = now.Add(10 * time.Minute)
	store.sessions = []domain.SessionRecord{{ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now}}
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.acked != 1 || len(delivery.submissions) != 1 || delivery.submissions[0].target != "replacement" {
		t.Fatalf("expired deferral re-claim: acked = %d, submissions = %#v", store.acked, delivery.submissions)
	}
}

// TestCoordinatorExpiredBatchWithoutOrchestratorStaysClaimable pins the issue
// #121 freeze: a batch whose availability expired while its project had no
// orchestrator must still be re-claimed — visibly, with retry state — instead
// of spinning as a silent pending head that starves every other project. A
// replacement orchestrator delivers it at the rescheduled availability.
func TestCoordinatorExpiredBatchWithoutOrchestratorStaysClaimable(t *testing.T) {
	now := time.Date(2026, 10, 21, 18, 23, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	// The checkpoint fallback deadline (+1h) expired with no orchestrator left.
	expired := reportAt("expired", "worker", now.Add(-time.Hour), now, "checkpoint context")
	store.add(expired)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})

	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatalf("expired batch without orchestrator: %v", err)
	}
	if store.reports[0].DeliveryAttempts == 0 {
		t.Fatal("expired available_at was never re-claimed: delivery_attempts stayed 0")
	}
	if store.reports[0].DeliveryState != domain.ReportPending || store.reports[0].LastError == "" {
		t.Fatalf("re-claim left no visible retry state: %+v", store.reports[0])
	}
	if !store.reports[0].AvailableAt.After(now) {
		t.Fatalf("re-claim did not reschedule availability: %+v", store.reports[0])
	}

	// The blocked project must not starve a due batch in another project.
	other := reportAt("other", "worker2", now, now, "other project")
	other.ProjectID = "q"
	store.add(other)
	store.sessions = []domain.SessionRecord{{ID: "orch-q", ProjectID: "q", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 || delivery.submissions[0].target != "orch-q" {
		t.Fatalf("starved submissions = %#v", delivery.submissions)
	}

	// A replacement orchestrator delivers the re-claimed batch at its next
	// availability.
	store.sessions = append(store.sessions, domain.SessionRecord{ID: "replacement", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(time.Second)})
	now = store.reports[0].AvailableAt
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.acked != 2 || len(delivery.submissions) != 2 || delivery.submissions[1].target != "replacement" {
		t.Fatalf("replacement delivery: acked = %d, submissions = %#v", store.acked, delivery.submissions)
	}
}

func TestCoordinatorSuppressesRepeatEmissionWithinEmitTTL(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("dup", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: errors.New("prompt submission was not observed")}
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	// First attempt: the pane write already showed the batch before semantic
	// acceptance failed, so the coordinator releases and schedules a retry.
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("first attempt should surface the submit error")
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(delivery.submissions))
	}
	// A retry minutes later, still unacked: the visible channel stays silent
	// and the batch waits out the emit TTL instead of re-emitting.
	now = now.Add(2 * time.Minute)
	delivery.submitErr = nil
	delay, err := c.RunDue(context.Background())
	if err != nil || delay <= 0 {
		t.Fatalf("silent retry delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: repeated attempt re-emitted the batch", len(delivery.submissions))
	}
	for _, rec := range store.reports {
		if rec.DeliveryState != domain.ReportPending || !rec.AvailableAt.After(now) {
			t.Fatalf("record = %+v, want pending deferred past the emit TTL", rec)
		}
	}
	if got := logs.String(); !strings.Contains(got, "repeat suppressed until emit TTL") || strings.Count(got, "report: batch emitted") != 1 {
		t.Fatalf("suppression evidence = %q", got)
	}
}

func TestCoordinatorEscalatesQuietWindowAndStopsReemission(t *testing.T) {
	// A sleeping recipient never acks, so every visible re-emission fails
	// acceptance and returns the batch to pending. The quiet window escalates
	// 15m -> 1h -> 6h -> stop instead of re-emitting every TTL forever (#127).
	now := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("sleeper", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: errors.New("prompt submission was not observed")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	ctx := context.Background()
	run := func(step time.Duration) {
		now = now.Add(step)
		c.RunDue(ctx)
	}
	run(0) // emission 1 at t0
	run(15 * time.Minute)
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2: first TTL re-emission must stay visible", len(delivery.submissions))
	}
	run(time.Minute) // attempts=3: quiet window grows to 1h, batch defers silently
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2: second cycle must stay silent", len(delivery.submissions))
	}
	run(59 * time.Minute) // 1h past the second emission: emission 3
	run(time.Minute)      // attempts=5: quiet window grows to 6h
	run(6 * time.Hour)    // emission 4
	run(time.Minute)      // attempts=7: visible re-emissions stop for good
	if len(delivery.submissions) != 4 {
		t.Fatalf("submissions = %d, want 4", len(delivery.submissions))
	}
	for i := 0; i < 3; i++ {
		run(6 * time.Hour)
		if len(delivery.submissions) != 4 {
			t.Fatalf("submissions = %d after +6h cycle %d, want 4: re-emission must be stopped", len(delivery.submissions), i+1)
		}
	}
	for _, rec := range store.reports {
		if rec.DeliveryState != domain.ReportPending {
			t.Fatalf("record = %+v, want the batch left pending for a silent wake ack", rec)
		}
	}
}

func TestCoordinatorEmissionLogsEvidence(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 30, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("evidence", "worker", now, now, "context"))
	var logs bytes.Buffer
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"report: batch emitted", "target=orch", "reports=1", "attempts=1"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("emission evidence missing %q: %q", want, logs.String())
		}
	}
}

func TestCoordinatorReemitsOnceAfterEmitTTL(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("noshow", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{submitErr: errors.New("prompt submission was not observed")}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("first attempt should surface the submit error")
	}
	// After the no-show TTL expires exactly one re-emission is allowed.
	now = now.Add(20 * time.Minute)
	if _, err := c.RunDue(context.Background()); err == nil {
		t.Fatal("re-emission should surface the submit error again")
	}
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2 after the emit TTL expired", len(delivery.submissions))
	}
	// The marker was refreshed by the re-emission: a retry inside the new
	// window stays silent instead of emitting on every attempt.
	now = now.Add(time.Minute)
	delivery.submitErr = nil
	if delay, err := c.RunDue(context.Background()); err != nil || delay <= 0 {
		t.Fatalf("post-re-emission delay = %v, err = %v", delay, err)
	}
	if len(delivery.submissions) != 2 {
		t.Fatalf("submissions = %d, want 2: refreshed marker did not suppress the repeat", len(delivery.submissions))
	}
}

func TestCoordinatorFencedAckRequeueDoesNotReemit(t *testing.T) {
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("fenced", "worker", now, now, "context"))
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(delivery.submissions))
	}
	// #104 fencing loses the ack and a daemon restart requeues the claimed
	// batch; the re-claim must not show the same content a second time.
	store.fenceAcks = true
	store.requeue()
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: requeued batch was re-emitted", len(delivery.submissions))
	}
}

func TestEmitVisibleAtMostOncePerTTL(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorStore(now)
	store.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	store.add(reportAt("piggy", "worker", now, now.Add(time.Hour), "context"))
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: &coordinatorDelivery{}, Now: func() time.Time { return now }, NewToken: func() string { return "tok" }})
	ctx := context.Background()
	batch, err := c.PreparePiggyback(ctx, "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("prepare = %#v, err = %v", batch, err)
	}
	if visible, err := c.EmitVisible(ctx, batch); err != nil || !visible {
		t.Fatalf("first emit visible = %v, err = %v", visible, err)
	}
	if store.reports[0].EmittedAt != now {
		t.Fatalf("emitted marker = %v, want %v", store.reports[0].EmittedAt, now)
	}
	// A re-claim of the same batch inside the window must ride along silently.
	if err := c.ReleasePiggyback(ctx, batch, errors.New("turn refused")); err != nil && !strings.Contains(err.Error(), "turn refused") {
		t.Fatal(err)
	}
	batch, err = c.PreparePiggyback(ctx, "orch")
	if err != nil || len(batch.Reports) != 1 {
		t.Fatalf("re-prepare = %#v, err = %v", batch, err)
	}
	if visible, err := c.EmitVisible(ctx, batch); err != nil || visible {
		t.Fatalf("repeat emit visible = %v, err = %v", visible, err)
	}
	if visible, err := c.EmitVisible(ctx, batch); err != nil || visible {
		t.Fatalf("suppressed batch re-marked visible = %v, err = %v", visible, err)
	}
	// Past the TTL one re-emission is allowed and refreshes the marker.
	now = now.Add(20 * time.Minute)
	if visible, err := c.EmitVisible(ctx, batch); err != nil || !visible {
		t.Fatalf("post-TTL emit visible = %v, err = %v", visible, err)
	}
	if got := store.reports[0].EmittedAt; !got.Equal(now) {
		t.Fatalf("emitted marker = %v, want refreshed to %v", got, now)
	}
}

type coordinatorStore struct {
	mu         sync.Mutex
	now        time.Time
	reports    []domain.ReportRecord
	sessions   []domain.SessionRecord
	interrupts map[domain.SessionID]time.Time
	acked      int
	fenceAcks  bool
}

func newCoordinatorStore(now time.Time) *coordinatorStore {
	return &coordinatorStore{now: now, interrupts: map[domain.SessionID]time.Time{}}
}

func reportAt(id string, worker domain.SessionID, created, available time.Time, note string) domain.ReportRecord {
	return domain.ReportRecord{ID: id, SessionID: worker, ProjectID: "p", State: domain.ReportCheckpoint, Note: note, CreatedAt: created, AvailableAt: available, DeliveryState: domain.ReportPending, RepeatCount: 1}
}

func (s *coordinatorStore) add(records ...domain.ReportRecord) {
	s.reports = append(s.reports, records...)
}
func (s *coordinatorStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return append([]domain.SessionRecord(nil), s.sessions...), nil
}
func (s *coordinatorStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
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
func (s *coordinatorStore) ClaimPendingReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) ([]domain.ReportRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batchID := ""
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending {
			batchID = s.reports[i].DeliveryBatchID
			if batchID == "" {
				batchID = "report-batch:" + s.reports[i].ID
			}
			break
		}
	}
	var claimed []domain.ReportRecord
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportPending {
			if s.reports[i].DeliveryBatchID != "" && s.reports[i].DeliveryBatchID != batchID {
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

// requeue mirrors the daemon-startup RequeueClaimedReports recovery: every
// claimed report returns to pending with a cleared token while its durable
// delivery_batch_id survives.
func (s *coordinatorStore) requeue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.reports {
		if s.reports[i].DeliveryState == domain.ReportClaimed {
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].ClaimedAt = domain.ReportPending, "", time.Time{}
		}
	}
}
func (s *coordinatorStore) AcknowledgeReportBatch(_ context.Context, project domain.ProjectID, token string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenceAcks {
		return 0, nil
	}
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
			s.reports[i].DeliveryState, s.reports[i].ClaimToken, s.reports[i].LastError = domain.ReportPending, "", reason
			count++
		}
	}
	return count, nil
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
func (s *coordinatorStore) MarkReportBatchEmitted(_ context.Context, project domain.ProjectID, token string, at time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for i := range s.reports {
		if s.reports[i].ProjectID == project && s.reports[i].DeliveryState == domain.ReportClaimed && s.reports[i].ClaimToken == token {
			s.reports[i].EmittedAt = at
			count++
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
	submissions []coordinatorSubmission
	interrupts  []domain.SessionID
	submitErr   error
}

func (d *coordinatorDelivery) Submit(_ context.Context, target domain.SessionID, message, key string) error {
	// A failed submit still showed its content on the visible channel (the
	// pane write precedes the acceptance wait), so it counts as an emission.
	d.submissions = append(d.submissions, coordinatorSubmission{target, message, key})
	return d.submitErr
}
func (d *coordinatorDelivery) Interrupt(_ context.Context, target domain.SessionID) error {
	d.interrupts = append(d.interrupts, target)
	return nil
}
