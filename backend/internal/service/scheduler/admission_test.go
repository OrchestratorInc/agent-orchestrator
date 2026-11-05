package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// 删除 Prepare 的资源/能力/任期门禁，旧 Workflow Claim 将通过本应闭锁的入口。
func TestLegacyClaimUsesSharedAdmissionAndFailsClosed(t *testing.T) {
	f := newFixture(t)
	w := f.create(4, domain.HarnessCodex)
	tasks := f.tasks(w)
	claim := func(i int) (workflow.ClaimResult, bool, error) {
		return f.workflow.Claim(context.Background(), workflow.ClaimInput{WorkflowID: w.ID, TaskID: tasks[i].ID, ExpectedRevision: tasks[i].Revision, WorkerID: "manual", IdempotencyKey: fmt.Sprintf("manual-%d", i)})
	}
	f.resource.cpu = 85
	if _, _, err := claim(0); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("hard resource gate: %v", err)
	}
	f.resource.cpu = 20
	for i := 0; i < 3; i++ {
		f.clock.Advance(SampleInterval)
		if err := f.scheduler.RefreshResources(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, created, err := claim(0); err != nil || !created {
		t.Fatalf("after recovery: %v %v", created, err)
	}
	f.resolver.RegisterProbeFunc(domain.HarnessCodex, func(_ context.Context, _ ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
		c := domain.NewHarnessCapabilitySnapshot(domain.HarnessCodex, domain.CapabilitySourceStatic, f.clock.Now(), time.Minute)
		c.AuthState, c.Readiness = domain.AgentAuthenticationAuthorized, domain.CapabilityReady
		return c, nil
	})
	if _, _, err := claim(1); !errors.Is(err, domain.ErrCapabilityEvidenceInsufficient) {
		t.Fatalf("static capability gate: %v", err)
	}
	f.clock.Advance(LeaderLease)
	if _, _, err := claim(1); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("expired leader: %v", err)
	}
}

// 删除每轮 ClaimLimit、Store 全局/Adapter 容量复核，会出现第四或第十一个占位。
func TestDefaultGlobalAndAdapterCapsAndTenthSlot(t *testing.T) {
	f := newFixture(t)
	f.create(12, domain.HarnessCodex)
	f.create(12, domain.HarnessClaudeCode)
	f.create(12, domain.HarnessKimi)
	for i := 0; i < 3; i++ {
		if got := len(f.tick().Dispatches); got > ClaimLimit {
			t.Fatalf("batch=%d", got)
		}
	}
	admissions, err := f.store.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(admissions) != 3 {
		t.Fatalf("default global: %d %v", len(admissions), err)
	}
	counts := map[domain.AgentHarness]int{}
	for _, a := range admissions {
		counts[a.Harness]++
	}
	for h, n := range counts {
		if n > 2 {
			t.Fatalf("adapter %s=%d", h, n)
		}
	}
	adapter := newFixture(t)
	adapter.create(6, domain.HarnessCodex)
	for i := 0; i < 4; i++ {
		adapter.tick()
	}
	adapterAdmissions, err := adapter.store.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(adapterAdmissions) != 2 {
		t.Fatalf("default adapter: %d %v", len(adapterAdmissions), err)
	}
	g := newFixture(t)
	g.policy("", 10)
	g.policy(domain.HarnessCodex, 10)
	g.create(15, domain.HarnessCodex)
	for i := 0; i < 12; i++ {
		if got := len(g.tick().Dispatches); got > 3 {
			t.Fatalf("batch=%d", got)
		}
	}
	admissions, err = g.store.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(admissions) != 10 {
		t.Fatalf("hard global: %d %v", len(admissions), err)
	}
	if got := len(g.tick().Dispatches); got != 0 {
		t.Fatalf("eleventh claim=%d", got)
	}
}

// 删除软线 MaxNewClaims=1 或采样间隔判断，会批准同轮第二个执行或重复读取资源。
func TestSoftPressureLimitsTickAndSamplingCadence(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	f.policy(domain.HarnessCodex, 10)
	f.resource.cpu = 75
	w := f.create(8, domain.HarnessCodex)
	l, _ := f.store.GetSchedulerLeader(context.Background())
	if ok, err := f.store.PutSchedulerWorkflowState(context.Background(), domain.SchedulerWorkflowState{WorkflowID: w.ID, Weight: 5}, 0, l.Fence, f.clock.Now()); err != nil || !ok {
		t.Fatal(err)
	}
	if out := f.tick(); len(out.Dispatches) != 1 || out.Capacity.MaxNewClaims != 1 {
		t.Fatalf("soft: %+v", out)
	}
	if err := f.scheduler.RefreshResources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.resource.calls != 1 {
		t.Fatalf("duplicate sample reads=%d", f.resource.calls)
	}
	f.clock.Advance(SampleInterval)
	f.resource.err = errors.New("sampler offline")
	if err := f.scheduler.RefreshResources(context.Background()); err == nil {
		t.Fatal("sampler failure hidden")
	}
	if out := f.tick(); len(out.Dispatches) != 0 || out.Capacity.Limit != 0 {
		t.Fatalf("sampler failure reused old capacity: %+v", out)
	}
}

// 删除持久任期/恢复门禁会使旧 epoch 或新但未恢复的调度器成功领取。
func TestRestartPreservesCursorAndRejectsOldEpoch(t *testing.T) {
	f := newFixture(t)
	f.policy("", 10)
	for _, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessKimi} {
		f.policy(h, 10)
		f.create(4, h)
	}
	first := f.tick()
	if len(first.Dispatches) != 3 {
		t.Fatalf("first batch: %+v", first)
	}
	before, _ := f.store.GetSchedulerLeader(context.Background())
	old := f.scheduler
	f.clock.Advance(LeaderLease + time.Second)
	f.connect("scheduler-2")
	if _, err := f.scheduler.Tick(context.Background()); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("unrecovered=%v", err)
	}
	if _, err := old.Tick(context.Background()); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch=%v", err)
	}
	f.recover()
	after, _ := f.store.GetSchedulerLeader(context.Background())
	if after.Cursor != before.Cursor || after.Fence.Epoch <= before.Fence.Epoch {
		t.Fatalf("restart changed cursor or not epoch: before=%+v after=%+v", before, after)
	}
	if got := f.tick(); len(got.Dispatches) == 0 || got.Dispatches[0].WorkflowID == domain.WorkflowID(before.Cursor) {
		t.Fatalf("rotation after restart: %+v cursor=%s", got, before.Cursor)
	}
}

// 删除轮次锁或统一原子准入会让同时 Tick 与旧 Claim 超出三个持久占位。
func TestConcurrentTickAndLegacyClaimShareCapacity(t *testing.T) {
	f := newFixture(t)
	f.policy(domain.HarnessCodex, 10)
	w := f.create(12, domain.HarnessCodex)
	tasks := f.tasks(w)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, err := f.scheduler.Tick(context.Background())
				errs <- err
				return
			}
			_, _, err := f.workflow.Claim(context.Background(), workflow.ClaimInput{WorkflowID: w.ID, TaskID: tasks[i].ID, ExpectedRevision: tasks[i].Revision, WorkerID: "legacy", IdempotencyKey: fmt.Sprintf("concurrent-%d", i)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && refusalReason(err) == "" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	admissions, err := f.store.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(admissions) != 3 {
		t.Fatalf("concurrent occupancy: %d %v", len(admissions), err)
	}
}

// 删除耗时能力调用之后的二次复核，旧采样或已过期任期仍会被交给 Claim。
func TestSlowCapabilityCannotReuseOldAdmissionTime(t *testing.T) {
	for _, elapsed := range []time.Duration{16 * time.Second, 31 * time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			f := newFixture(t)
			w := f.create(1, domain.HarnessCodex)
			f.resolver.RegisterProbeFunc(domain.HarnessCodex, func(_ context.Context, req ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
				observed := f.clock.Now()
				f.clock.Advance(elapsed)
				c := domain.NewHarnessCapabilitySnapshot(req.Harness, domain.CapabilitySourceConfirmedRuntime, observed, time.Minute)
				c.AuthState, c.Readiness = domain.AgentAuthenticationAuthorized, domain.CapabilityReady
				return c, nil
			})
			_, err := f.scheduler.PrepareWorkflowAdmission(context.Background(), f.tasks(w)[0])
			want := domain.ErrSchedulerCapacity
			if elapsed > LeaderLease {
				want = domain.ErrSchedulerFence
			}
			if !errors.Is(err, want) {
				t.Fatalf("elapsed %s: %v, want %v", elapsed, err, want)
			}
		})
	}
}
