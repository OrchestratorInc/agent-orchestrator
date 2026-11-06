package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/budget"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// m3Clock 在 HTTP 与 Controller goroutine 间保持可推进的同一个时钟。
type m3Clock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 让跨 goroutine 的观察使用同一个完整时间值。
func (c *m3Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

// advance 显式模拟续租周期和停机时间，不等待真实分钟流逝。
func (c *m3Clock) advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

// m3Resources 替代主机负载读取，真实 Scheduler 决定资源是否允许新增执行。
type m3Resources struct {
	clock     *m3Clock
	mu        sync.Mutex
	cpu       uint64
	available uint64
	err       error
}

// Sample 生成可验证的离线 CPU/RAM 差量，采样失败保持为错误。
func (r *m3Resources) Sample(context.Context) (domain.ResourceSample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	return domain.ResourceSample{ObservedAt: now, PreviousAt: now.Add(-5 * time.Second),
		TotalBytes: 16 << 30, AvailableBytes: r.available, CPUTotalTicks: 200, CPUBusyTicks: 10 + r.cpu,
		PreviousCPUTotalTicks: 100, PreviousCPUBusyTicks: 10}, r.err
}

// load 原子切换下次观测，避免 HTTP 和测试设置间的数据竞争。
func (r *m3Resources) load(cpu uint64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cpu, r.err = cpu, err
}

// memory 独立改变 RAM 观测，不改变 CPU 数据或复用上一次采样。
func (r *m3Resources) memory(available uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.available = available
}

// m3Control 持有每次启动自己的 Scheduler；重启只能经真实 Recovery 接管持久占位。
type m3Control struct {
	store        *store.Store
	clock        *m3Clock
	resources    *m3Resources
	scheduler    *scheduler.Service
	workflows    *workflow.Service
	binder       *execution.Coordinator
	manager      *sessionmanager.Manager
	capabilities *capability.Resolver
	budget       *budget.Service
	quality      *m4Services
}

// newM3Control 获取本实例任期；观测可替换，准入仍由真实服务裁决。
func newM3Control(t *testing.T, st *store.Store, clock *m3Clock, owner string) *m3Control {
	t.Helper()
	c := &m3Control{store: st, clock: clock}
	c.resources = &m3Resources{clock: clock, cpu: 20, available: 12 << 30}
	resolver := capability.NewResolver(capability.WithClock(clock.Now))
	c.capabilities = resolver
	resolver.RegisterProbeFunc(domain.HarnessCodex, func(_ context.Context, req ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error) {
		s := domain.NewHarnessCapabilitySnapshot(req.Harness, domain.CapabilitySourceConfirmedRuntime, clock.Now(), time.Minute)
		s.Readiness, s.AuthState, s.Surfaces = domain.CapabilityReady, domain.AgentAuthenticationAuthorized, domain.CapabilitySurfaceBoth
		return s, nil
	})
	c.scheduler = scheduler.New(scheduler.Deps{Store: st, Tasks: st, OwnerID: owner, Clock: clock.Now,
		Resources: c.resources, Capabilities: resolver,
		Claim: func(ctx context.Context, in workflow.ClaimInput) (workflow.ClaimResult, bool, error) {
			return c.workflows.Claim(ctx, in)
		},
	})
	if _, err := c.scheduler.MaintainLeadership(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.budget = budget.New(st, clock.Now)
	return c
}

// wire 在开放 HTTP 前执行真实恢复，保持 daemon 的先恢复后准入顺序。
func (c *m3Control) wire(t *testing.T, deps *httpd.APIDeps, workflows *workflow.Service, binder *execution.Coordinator) {
	t.Helper()
	c.workflows, c.binder = workflows, binder
	ctx := context.Background()
	l, err := c.store.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := binder.RecoverAdmissions(ctx, l.Fence); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.store.CompleteSchedulerRecovery(ctx, l.Fence, l.Revision, c.clock.Now()); err != nil || !ok {
		t.Fatalf("recovery: applied=%v err=%v", ok, err)
	}
	deps.Scheduler, deps.Budget = c.scheduler, c.budget
	deps.WorkflowMaterializer, deps.WorkflowMaterializations, deps.WorkflowRecovery = workflows, c.store, c.store
	if c.quality != nil {
		c.quality.wire(t, c, deps)
	}
}

// newM3Fixture 复用真实 Manager/Git/Router，将所有持久数据放在测试临时目录。
func newM3Fixture(t *testing.T) *m2ffFixture {
	t.Helper()
	data := t.TempDir()
	st := sqlitetest.MustOpenAt(t, data)
	// 固定历史时间让漏接时钟的许可校验立即失败，不再依赖测试是否耗尽真实 30 秒租期。
	c := newM3Control(t, st, &m3Clock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}, "m3-boot-1")
	return newM2FFFixture(t, m2ffPlant{store: st, dataDir: data, managedDir: filepath.Join(data, "worktrees"),
		repoA: m2smGitRepo(t), repoB: m2smGitRepo(t), m3: c})
}

// m3JSON 序列化请求类型，避免测试重复手维护控制面字段契约。
func m3JSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// m3Policy 通过真实 HTTP 的独立人工审批修改并发限制。
func m3Policy(t *testing.T, f *m2ffFixture, harness domain.AgentHarness, limit int64) {
	t.Helper()
	p := domain.DefaultSchedulerPolicy(harness)
	policies, err := f.store.ListSchedulerPolicies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range policies {
		if current.ID == p.ID {
			p = current
		}
	}
	body := m3JSON(t, controllers.PutSchedulerPolicyRequest{
		OperatorChangeRequest: controllers.OperatorChangeRequest{ExpectedRevision: p.Revision, IdempotencyKey: fmt.Sprintf("policy-%s-%d", p.ID, limit), Reason: "TEST_CAPACITY"},
		Harness:               string(harness), MaxConcurrent: limit, CPURecoverPercent: p.CPURecoverPercent, CPUSoftPercent: p.CPUSoftPercent,
		CPUHardPercent: p.CPUHardPercent, RAMSoftPercent: p.RAMSoftPercent, RAMHardPercent: p.RAMHardPercent, RAMFloorBytes: p.RAMFloorBytes,
	})
	res := f.asOperator(http.MethodPut, "/scheduler/policy", body)
	if res.Status != http.StatusOK {
		t.Fatalf("policy: %d %s", res.Status, res.Raw)
	}
}

// m3Create 为容量/公平性用例分配互不重叠的写范围，避免被 M4 路径占位先行拦截。
func m3Create(t *testing.T, f *m2ffFixture, key string, count int) domain.WorkflowID {
	t.Helper()
	tasks, repos := []map[string]any{}, []map[string]any{}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%s-task-%02d", key, i)
		tasks = append(tasks, map[string]any{"id": id, "kind": "development", "title": id, "agentHarness": domain.HarnessCodex, "maxAttempts": 3})
		repos = append(repos, map[string]any{"taskId": id, "repoKey": "repo-a", "allowedPaths": []string{id}})
	}
	body := m3JSON(t, map[string]any{"title": key, "goal": "M3真实装配验收", "idempotencyKey": key,
		"repositories": []map[string]any{{"repoKey": "repo-a", "projectId": f.projectA}}, "tasks": tasks, "taskRepositories": repos})
	r := f.asOperator(http.MethodPost, "/workflows", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	id := domain.WorkflowID(f.text(r.Body, "workflow", "id"))
	r = f.asOperator(http.MethodPost, "/workflows/"+string(id)+"/activate", fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":%q}`, f.number(r.Body, "workflow", "revision"), key+"-activate"))
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Raw)
	}
	return id
}

// m3Dispatch 只启动 Scheduler 本轮成功授予的 Attempt，并由真实 Coordinator/Manager 建立工作区。
func m3Dispatch(t *testing.T, f *m2ffFixture) []scheduler.Dispatch {
	t.Helper()
	f.m3.clock.advance(5 * time.Second)
	if _, err := f.m3.scheduler.MaintainLeadership(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := f.m3.scheduler.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m3StartDispatches(t, f, out.Dispatches)
	return out.Dispatches
}

// m3StartDispatches 消费调度输出，不生成额外 Claim；双调度器场景也使用同一真实启动路径。
func m3StartDispatches(t *testing.T, f *m2ffFixture, dispatches []scheduler.Dispatch) {
	t.Helper()
	for _, d := range dispatches {
		base := f.attemptPath(d.WorkflowID, d.AttemptID, "start")
		r := f.asOperator(http.MethodPost, base, fmt.Sprintf(`{"idempotencyKey":%q}`, "start-"+string(d.AttemptID)))
		if r.Status != http.StatusOK {
			t.Fatalf("start: %d %s", r.Status, r.Raw)
		}
		r = f.asOperator(http.MethodPost, f.bindPath(d.WorkflowID, d.AttemptID), fmt.Sprintf(`{"harness":"codex","mode":"chat","prompt":"hold","idempotencyKey":%q}`, "bind-"+string(d.AttemptID)))
		if r.Status != http.StatusOK {
			t.Fatalf("bind: %d %s", r.Status, r.Raw)
		}
	}
}
