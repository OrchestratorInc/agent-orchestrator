package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestM3LeaseRenewalAndRestartFence 删除可信观测续租、UNKNOWN 保留占位或任期复核，会终止长任务或重复启动。
func TestM3LeaseRenewalAndRestartFence(t *testing.T) {
	f := newM3Fixture(t)
	ctx := context.Background()
	wid := m3Create(t, f, "recovery", 1)
	dispatched := m3Dispatch(t, f)
	if len(dispatched) != 1 {
		t.Fatalf("dispatches=%d", len(dispatched))
	}
	aid := dispatched[0].AttemptID
	sid := *f.binding(aid).SessionID
	original, found, err := f.store.GetWorkflowAttempt(ctx, aid)
	if err != nil || !found {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		f.m3.clock.advance(20 * time.Second)
		leader, err := f.m3.scheduler.MaintainLeadership(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.m3.binder.RefreshLeases(ctx, leader.Fence); err != nil {
			t.Fatal(err)
		}
	}
	current, _, err := f.store.GetWorkflowAttempt(ctx, aid)
	if err != nil || current.State != domain.WorkflowAttemptStateRunning || !current.LeaseUntil.After(*original.LeaseUntil) || !current.LeaseUntil.After(f.m3.clock.Now()) {
		t.Fatalf("live long task lease: original=%v current=%+v err=%v", original.LeaseUntil, current, err)
	}
	fence, err := f.m3.scheduler.CurrentFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldPermit, err := f.store.LoadExecutionPermit(ctx, aid, fence, f.m3.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.chat.StopAll(ctx)
	f.m3.clock.advance(3 * time.Minute)
	if _, err := f.store.RecoverExpiredWorkflowAttempts(ctx, f.m3.clock.Now()); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	next := newM3Control(t, reopened, f.m3.clock, "m3-boot-2")
	second := newM2FFFixture(t, m2ffPlant{store: reopened, dataDir: f.dataDir, managedDir: f.managedDir, repoA: f.repoA, repoB: f.repoB, m3: next})
	ads, err := reopened.ListSchedulerAdmissions(ctx, true)
	if err != nil || len(ads) != 1 || ads[0].SessionID != sid || ads[0].State != domain.AdmissionUncertain || ads[0].Fence.Epoch <= fence.Epoch {
		t.Fatalf("restart occupancy: %+v err=%v", ads, err)
	}
	if len(second.sessionsFor()) != 1 || second.chat.HasLiveChatController(sid) {
		t.Fatal("restart spawned or relaunched an uncertain session")
	}
	if err := f.store.ValidateExecutionPermit(ctx, oldPermit, f.m3.clock.Now()); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch permit accepted: %v", err)
	}
	if _, err := f.m3.binder.Resume(ports.WithExecutionPermit(ctx, oldPermit), aid); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old coordinator resumed: %v", err)
	}
	if r := f.asOperator(http.MethodPost, f.attemptPath(wid, aid, "start"), `{"idempotencyKey":"old-owner"}`); r.Status != http.StatusConflict || f.text(r.Body, "code") != "SCHEDULER_FENCE_STALE" {
		t.Fatalf("old HTTP borrowed new owner: %d %s", r.Status, r.Raw)
	}
	f.srv.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	res := second.asOperator(http.MethodPost, second.bindPath(wid, aid), fmt.Sprintf(`{"harness":"codex","mode":"chat","prompt":"hold","idempotencyKey":%q}`, "bind-"+string(aid)))
	if res.Status != http.StatusConflict {
		t.Fatalf("UNKNOWN rebind: %d %s", res.Status, res.Raw)
	}
	if err := next.binder.RefreshLeases(ctx, ads[0].Fence); err != nil {
		t.Fatal(err)
	}
	after, _, err := reopened.GetWorkflowAttempt(ctx, aid)
	if err != nil || after.State != domain.WorkflowAttemptStateUnknown || len(second.sessionsFor()) != 1 || second.chat.HasLiveChatController(sid) {
		t.Fatalf("UNKNOWN was renewed or spawned: %+v err=%v", after, err)
	}
}
