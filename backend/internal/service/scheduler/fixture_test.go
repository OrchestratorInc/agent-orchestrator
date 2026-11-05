package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// fakeClock 在并发 Claim 与采样之间共享一个确定时钟。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 保证测试观察到完整时间值。
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 用显式推进替代等待真实时间。
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeResource 只生成离线 CPU/RAM 事实，不读取主机负载。
type fakeResource struct {
	clock *fakeClock
	cpu   uint64
	err   error
	calls int
}

// Sample 将测试设定的负载或失败交给真实准入门禁。
func (r *fakeResource) Sample(context.Context) (domain.ResourceSample, error) {
	r.calls++
	sample := healthyResource(r.clock.Now())
	sample.CPUBusyTicks = sample.PreviousCPUBusyTicks + r.cpu
	return sample, r.err
}

// fixture 装配真实 SQLite、Workflow 与 Scheduler，仅时钟和能力观测为假。
type fixture struct {
	t         *testing.T
	store     *sqlite.Store
	clock     *fakeClock
	resource  *fakeResource
	scheduler *Service
	workflow  *workflow.Service
	resolver  *capability.Resolver
	idMu      sync.Mutex
	sequence  int
}

// newFixture 使用测试临时数据库，不接触用户数据或任何真实 Provider。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, store: sqlitetest.MustOpen(t), clock: &fakeClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}}
	f.resource = &fakeResource{clock: f.clock, cpu: 20}
	f.resolver = capability.NewResolver(capability.WithClock(f.clock.Now))
	for _, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessKimi} {
		f.resolver.RegisterProbeFunc(h, func(_ context.Context, req ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
			c := domain.NewHarnessCapabilitySnapshot(req.Harness, domain.CapabilitySourceConfirmedRuntime, f.clock.Now(), time.Minute)
			c.AuthState, c.Readiness, c.Surfaces = domain.AgentAuthenticationAuthorized, domain.CapabilityReady, domain.CapabilitySurfaceBoth
			return c, nil
		})
	}
	if err := f.store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "project", Path: t.TempDir(), RegisteredAt: f.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	f.connect("scheduler-1")
	f.recover()
	return f
}

// connect 为重启用例创建独立服务实例并显式取得新任期。
func (f *fixture) connect(owner string) {
	f.t.Helper()
	var workflowService *workflow.Service
	f.scheduler = New(Deps{Store: f.store, Tasks: f.store, Clock: f.clock.Now, OwnerID: owner, Resources: f.resource, Capabilities: f.resolver,
		Claim: func(ctx context.Context, in workflow.ClaimInput) (workflow.ClaimResult, bool, error) {
			return workflowService.Claim(ctx, in)
		}})
	workflowService = workflow.New(workflow.Deps{Store: f.store, Admission: f.scheduler, Clock: f.clock.Now, NewID: func() string {
		f.idMu.Lock()
		defer f.idMu.Unlock()
		f.sequence++
		return fmt.Sprintf("%04d", f.sequence)
	},
		GraphLimits: domain.WorkflowGraphLimits{MaxTasks: 1000, MaxDepth: 1000, MaxFanOut: 1000}, LeaseDuration: 15 * time.Minute})
	f.workflow = workflowService
	if _, err := f.scheduler.MaintainLeadership(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

// recover 明确模拟外部对账完成；测试不将这个调用当成真实生命周期恢复验证。
func (f *fixture) recover() {
	f.t.Helper()
	l, err := f.store.GetSchedulerLeader(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, ok, err := f.store.CompleteSchedulerRecovery(context.Background(), l.Fence, l.Revision, f.clock.Now()); err != nil || !ok {
		f.t.Fatalf("recovery: %v %v", ok, err)
	}
}

// create 通过正常领域服务创建及激活测试 DAG。
func (f *fixture) create(count int, harness domain.AgentHarness) domain.Workflow {
	f.t.Helper()
	in := workflow.CreateInput{Title: "Workflow", Goal: "Offline scheduler behavior", Repositories: []workflow.RepositoryInput{{RepoKey: "repo", ProjectID: "project"}}}
	for i := 0; i < count; i++ {
		in.Tasks = append(in.Tasks, workflow.TaskInput{ID: domain.WorkflowTaskID(fmt.Sprintf("task-%04d-%04d", f.sequence, i)), Title: "Task", Kind: domain.WorkflowTaskKindDevelopment, AgentHarness: harness})
	}
	w, _, err := f.workflow.Create(context.Background(), fmt.Sprintf("create-%d", f.sequence), in)
	if err != nil {
		f.t.Fatal(err)
	}
	w, _, err = f.workflow.Activate(context.Background(), w.ID, w.Revision, "activate-"+string(w.ID))
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

// policy 用真实策略 CAS 显式调整测试容量。
func (f *fixture) policy(h domain.AgentHarness, max int64) {
	f.t.Helper()
	p := domain.DefaultSchedulerPolicy(h)
	p.MaxConcurrent = max
	if _, ok, err := f.store.PutSchedulerPolicy(context.Background(), p, p.Revision, f.change(p.ID)); err != nil || !ok {
		f.t.Fatalf("policy: %v %v", ok, err)
	}
}

func (f *fixture) change(id string) domain.PolicyChange {
	return domain.PolicyChange{IdempotencyKey: "change-" + id, RequestFingerprint: strings.Repeat("a", 64), ActorRef: "offline-test-operator", Reason: "TEST_POLICY", Now: f.clock.Now()}
}

// tick 不隐藏基础设施错误，任何未分类失败都会终止用例。
func (f *fixture) tick() TickResult {
	f.t.Helper()
	out, err := f.scheduler.Tick(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// tasks 从持久化定义中取得当前 revision。
func (f *fixture) tasks(w domain.Workflow) []domain.WorkflowTask {
	f.t.Helper()
	tasks, err := f.store.ListWorkflowTasks(context.Background(), w.ID, "", 100, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return tasks
}

// permit 从真实 Store 读取内部执行快照，调度测试不跳过新写栅栏。
func (f *fixture) permit(id domain.WorkflowAttemptID) context.Context {
	f.t.Helper()
	ctx := context.Background()
	leader, err := f.store.GetSchedulerLeader(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	permit, err := f.store.LoadExecutionPermit(ctx, id, leader.Fence, f.clock.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return ports.WithExecutionPermit(ctx, permit)
}
