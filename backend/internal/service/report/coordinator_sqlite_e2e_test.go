package report_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/report"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// End-to-end through durable sqlite: create → stamp separate batch ids →
// Coordinator.RunDue with a permanently failing older batch must still deliver
// the newer due batch in the same cycle (#6119).
func TestCoordinatorSQLiteStarvationE2E(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "ao", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	orch, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "ao", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
		Harness: domain.HarnessClaudeCode, CreatedAt: now, UpdatedAt: now,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	workerA, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		CreatedAt: now, UpdatedAt: now,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	workerB, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}

	oldAt := now.Add(-2 * time.Minute)
	if _, err := store.CreateReport(ctx, domain.ReportRecord{
		ID: "rpt_old", SessionID: workerA.ID, ProjectID: "ao", Message: "old undeliverable",
		CreatedAt: oldAt, DeliveryState: domain.ReportPending, AvailableAt: oldAt, RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// Stamp a stable batch id on the older row before the newer report exists,
	// matching production retry identity (Assign only fills empty batch ids).
	if _, err := store.ClaimPendingReportBatch(ctx, "ao", "seed-token", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReleaseReportBatch(ctx, "ao", "seed-token", oldAt, ""); err != nil {
		t.Fatal(err)
	}
	seeded, ok, err := store.GetReport(ctx, "rpt_old")
	if err != nil || !ok || seeded.DeliveryBatchID == "" || seeded.DeliveryState != domain.ReportPending {
		t.Fatalf("seeded old = %+v ok=%v err=%v", seeded, ok, err)
	}

	newerAt := now.Add(-time.Minute)
	if _, err := store.CreateReport(ctx, domain.ReportRecord{
		ID: "rpt_newer", SessionID: workerB.ID, ProjectID: "ao", State: domain.ReportDone,
		Note: "newer done", CreatedAt: newerAt, DeliveryState: domain.ReportPending,
		AvailableAt: newerAt, SettlementDeadline: newerAt, RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	errSemantic := errors.New("session: semantic message acceptance unsupported")
	delivery := &e2eDelivery{failKeys: map[string]error{seeded.DeliveryBatchID: errSemantic}}
	c := report.NewCoordinator(report.CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now:        func() time.Time { return now },
		NewToken:   func() string { return "e2e-token" },
		RetryDelay: 5 * time.Second,
	})

	delay, runErr := c.RunDue(ctx)
	if runErr != nil {
		t.Fatalf("RunDue err = %v, want nil after newer succeeds", runErr)
	}
	if delay != 5*time.Second {
		t.Fatalf("delay = %v, want deferred older batch", delay)
	}

	old, ok, err := store.GetReport(ctx, "rpt_old")
	if err != nil || !ok {
		t.Fatalf("old get: ok=%v err=%v", ok, err)
	}
	newer, ok, err := store.GetReport(ctx, "rpt_newer")
	if err != nil || !ok {
		t.Fatalf("newer get: ok=%v err=%v", ok, err)
	}
	// seed claim + delivery claim
	if old.DeliveryAttempts != 2 || old.DeliveryState != domain.ReportPending {
		t.Fatalf("old = %+v", old)
	}
	if old.LastError != errSemantic.Error() {
		t.Fatalf("old last_error = %q", old.LastError)
	}
	if !old.AvailableAt.Equal(now.Add(5 * time.Second)) {
		t.Fatalf("old available_at = %s, want backoff", old.AvailableAt)
	}
	if newer.DeliveryAttempts != 1 || newer.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("newer = %+v", newer)
	}
	if len(delivery.keys) != 2 || delivery.keys[0] != seeded.DeliveryBatchID {
		t.Fatalf("submit order = %v, want old batch %q first", delivery.keys, seeded.DeliveryBatchID)
	}
	if delivery.targets[1] != orch.ID {
		t.Fatalf("newer target = %s, want orchestrator %s", delivery.targets[1], orch.ID)
	}
}

func TestCoordinatorSQLiteNoOrchestratorDoesNotStarveOtherProjectE2E(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	for _, id := range []string{"pa", "pb"} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: t.TempDir(), RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "pb", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat,
		Harness: domain.HarnessClaudeCode, CreatedAt: now, UpdatedAt: now,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	wa, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "pa", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		CreatedAt: now, UpdatedAt: now,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	wb, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "pb", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		CreatedAt: now, UpdatedAt: now,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateReport(ctx, domain.ReportRecord{
		ID: "blocked", SessionID: wa.ID, ProjectID: "pa", Message: "blocked",
		CreatedAt: now.Add(-2 * time.Minute), DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(-2 * time.Minute), RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateReport(ctx, domain.ReportRecord{
		ID: "ok", SessionID: wb.ID, ProjectID: "pb", Message: "ok",
		CreatedAt: now.Add(-time.Minute), DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(-time.Minute), RepeatCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	delivery := &e2eDelivery{}
	c := report.NewCoordinator(report.CoordinatorDeps{
		Store: store, Delivery: delivery,
		Now: func() time.Time { return now }, NewToken: func() string { return "tok" }, RetryDelay: 5 * time.Second,
	})
	delay, runErr := c.RunDue(ctx)
	if runErr != nil {
		t.Fatalf("RunDue err = %v", runErr)
	}
	if delay != 5*time.Second {
		t.Fatalf("delay = %v", delay)
	}
	blocked, _, _ := store.GetReport(ctx, "blocked")
	okRpt, _, _ := store.GetReport(ctx, "ok")
	if blocked.DeliveryAttempts != 1 || blocked.DeliveryState != domain.ReportPending {
		t.Fatalf("blocked = %+v", blocked)
	}
	if !blocked.AvailableAt.Equal(now.Add(5 * time.Second)) {
		t.Fatalf("blocked available_at = %s", blocked.AvailableAt)
	}
	if okRpt.DeliveryState != domain.ReportAcknowledged || okRpt.DeliveryAttempts != 1 {
		t.Fatalf("ok = %+v", okRpt)
	}
}

type e2eDelivery struct {
	keys     []string
	targets  []domain.SessionID
	failKeys map[string]error
}

func (d *e2eDelivery) Submit(_ context.Context, target domain.SessionID, _, key string) error {
	d.keys = append(d.keys, key)
	d.targets = append(d.targets, target)
	if err, ok := d.failKeys[key]; ok {
		return err
	}
	return nil
}

func (d *e2eDelivery) Interrupt(context.Context, domain.SessionID) error { return nil }
