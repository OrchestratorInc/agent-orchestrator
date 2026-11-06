package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestM4IntegrationOperationFencingAndRecovery 删除唯一领取、revision 栅栏或锁内对账会允许双执行及恢复后迟到写入。
func TestM4IntegrationOperationFencingAndRecovery(t *testing.T) {
	x, plan, entries := newIntegrationPlanFixture(t)
	ctx := context.Background()
	if _, err := x.s.CreateIntegrationPlan(ctx, plan, entries, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	req := domain.IntegrationApplyRequest{IdempotencyKey: "operation", ExpectedRevision: plan.Revision, PlanDigest: plan.PlanDigest, Entries: entries}
	op := domain.IntegrationOperation{ID: req.IdempotencyKey, PlanID: plan.ID, WorkflowID: plan.WorkflowID, Action: "apply", RequestDigest: "digest", Fence: x.ad.Fence}
	if _, _, err := x.s.BeginIntegrationOperation(ctx, op, req, x.now, func() (string, error) { return "", errors.New("no approval") }); err == nil {
		t.Fatal("missing approval accepted")
	}
	if _, found, _ := x.s.GetIntegrationOperation(ctx, op.ID); found {
		t.Fatal("unapproved intent persisted")
	}
	other, err := sqlite.Open(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var approvals, created atomic.Int32
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, s := range []*sqlite.Store{x.s, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, fresh, err := s.BeginIntegrationOperation(ctx, op, req, x.now, func() (string, error) { approvals.Add(1); return "operator", nil })
			errs[i] = err
			if fresh {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if approvals.Load() != 1 || created.Load() != 1 {
		t.Fatalf("approvals=%d created=%d", approvals.Load(), created.Load())
	}
	op, _, err = x.s.GetIntegrationOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.RequestIntegrationRepo(ctx, op, entries[1].RepoKey, x.now); err == nil {
		t.Fatal("second repository ran first")
	}
	if err := x.s.RequestIntegrationRepo(ctx, op, entries[0].RepoKey, x.now); err != nil {
		t.Fatal(err)
	}
	writes := 0
	old := op
	old.Fence.Epoch--
	if err := x.s.WithIntegrationWrite(ctx, old, entries[0], func() time.Time { return x.now }, func(context.Context) error { writes++; return nil }); err == nil || writes != 0 {
		t.Fatalf("old epoch wrote: %v %d", err, writes)
	}
	// 外部写已发生但进程未收到回执，恢复只能观察，不再次调用写回调。
	if err := x.s.WithIntegrationWrite(ctx, op, entries[0], func() time.Time { return x.now }, func(context.Context) error { writes++; return errors.New("lost Git receipt") }); err == nil {
		t.Fatal("lost receipt passed")
	}
	x.now = x.now.Add(time.Hour)
	leader, acquired, err := other.AcquireSchedulerLeader(ctx, "restarted-leader", x.now, x.now.Add(time.Minute))
	if err != nil || !acquired {
		t.Fatalf("acquire new leader=%v %v", acquired, err)
	}
	leader, _, err = other.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, x.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ReconcileIntegrationOperation(ctx, op.ID, op.Revision, leader.Fence, func() time.Time { return x.now }, func(context.Context, domain.WorkflowRepoKey) (string, error) {
		t.Fatal("stale approved revision observed Git")
		return "", nil
	}); !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("stale approved revision accepted: %v", err)
	}
	current, _, err := other.GetIntegrationOperation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := other.ReconcileIntegrationOperation(ctx, op.ID, current.Revision, leader.Fence, func() time.Time { return x.now }, func(context.Context, domain.WorkflowRepoKey) (string, error) { return entries[0].MergeCommitSHA, nil })
	if err != nil || recovered.State != "partial" {
		t.Fatalf("recovered=%+v %v", recovered, err)
	}
	if err := x.s.WithIntegrationWrite(ctx, op, entries[0], func() time.Time { return x.now }, func(context.Context) error { writes++; return nil }); err == nil || writes != 1 {
		t.Fatalf("late write=%d %v", writes, err)
	}
	rows, err := other.ListIntegrationRepoOperations(ctx, op.ID)
	if err != nil || len(rows) != 2 || rows[0].State != "applied" || rows[1].State != "rejected" {
		t.Fatalf("rows=%+v %v", rows, err)
	}
	op.Fence = leader.Fence
	if _, fresh, err := other.BeginIntegrationOperation(ctx, op, req, x.now, func() (string, error) { t.Fatal("replay consumed approval"); return "", nil }); err != nil || fresh {
		t.Fatalf("replay=%v %v", fresh, err)
	}
}
