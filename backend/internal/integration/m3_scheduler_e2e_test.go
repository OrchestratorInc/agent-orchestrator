package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
)

// TestM3ContendingSchedulersAndFairRotation 删除任期门禁或持久游标轮转，会使重复领取或高权重工作流遮住低权重任务。
func TestM3ContendingSchedulersAndFairRotation(t *testing.T) {
	f := newM3Fixture(t)
	ctx := context.Background()
	m3Create(t, f, "contended", 1)
	rival := scheduler.New(scheduler.Deps{Store: f.store, Tasks: f.store, OwnerID: "m3-rival", Clock: f.m3.clock.Now,
		Resources: f.m3.resources, Capabilities: f.m3.capabilities, Claim: f.m3.workflows.Claim})
	type result struct {
		out scheduler.TickResult
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, svc := range []*scheduler.Service{f.m3.scheduler, rival} {
		wg.Add(1)
		go func(s *scheduler.Service) {
			defer wg.Done()
			<-start
			if _, err := s.MaintainLeadership(ctx); err != nil {
				results <- result{err: err}
				return
			}
			out, err := s.Tick(ctx)
			results <- result{out: out, err: err}
		}(svc)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, refusals := 0, 0
	for res := range results {
		if errors.Is(res.err, domain.ErrSchedulerFence) {
			refusals++
			continue
		}
		if res.err != nil {
			t.Fatal(res.err)
		}
		if len(res.out.Dispatches) != 1 {
			t.Fatalf("winner dispatches=%d", len(res.out.Dispatches))
		}
		successes++
		m3StartDispatches(t, f, res.out.Dispatches)
	}
	if successes != 1 || refusals != 1 {
		t.Fatalf("competing schedulers: success=%d refused=%d", successes, refusals)
	}
	m3AssertOccupancy(t, f, 1)
	m3Policy(t, f, "", 10)
	m3Policy(t, f, domain.HarnessCodex, 10)
	// 策略升到 10 后仍须经过真实资源滞回，先取得足够容量再验证两轮 5:1 分配。
	for i := 0; i < 8; i++ {
		f.m3.clock.advance(scheduler.SampleInterval)
		if _, err := f.m3.scheduler.MaintainLeadership(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.m3.scheduler.RefreshResources(ctx); err != nil {
			t.Fatal(err)
		}
	}
	status, err := f.m3.scheduler.Status(ctx)
	if err != nil || status.Capacity.Limit != 10 {
		t.Fatalf("capacity before fair rotation: %+v %v", status.Capacity, err)
	}
	high := m3Create(t, f, "high-weight", 9)
	low := m3Create(t, f, "low-weight", 1)
	fence, err := f.m3.scheduler.CurrentFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.PutSchedulerWorkflowState(ctx, domain.SchedulerWorkflowState{WorkflowID: high, Weight: 5}, 0, fence, f.m3.clock.Now()); err != nil || !ok {
		t.Fatalf("weight: %v %v", ok, err)
	}
	leader, err := f.store.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 固定从低权重工作流之后绕回，保证高权重先被检查，避免 UUID 顺序掩盖饥饿。
	if ok, err := f.store.SetSchedulerCursor(ctx, fence, leader.Revision, string(low), f.m3.clock.Now()); err != nil || !ok {
		t.Fatalf("rotation start: %v %v", ok, err)
	}
	for i := 0; i < 2; i++ {
		m3Dispatch(t, f)
	}
	ads, err := f.store.ListSchedulerAdmissions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[domain.WorkflowID]int{}
	for _, ad := range ads {
		counts[ad.WorkflowID]++
	}
	if counts[high] != 5 || counts[low] != 1 {
		t.Fatalf("high weight starved another workflow: %+v", counts)
	}
	if len(f.sessionsFor()) != len(ads) {
		t.Fatal("scheduler admission did not reach the real manager")
	}
}
