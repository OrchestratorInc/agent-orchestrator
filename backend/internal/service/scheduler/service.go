// Package scheduler 只授予执行资格；进程、Git 与供应商副作用仍由 Execution Coordinator 持有。
package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

const (
	SampleInterval = 5 * time.Second
	ScanInterval   = 15 * time.Second
	LeaderLease    = 30 * time.Second
	LeaderRenewal  = 10 * time.Second
	CandidateLimit = 100
	ClaimLimit     = 3
)

// CandidateStore 按 Workflow 游标分页，避免一个大工作流遮住其他工作流。
type CandidateStore interface {
	ListSchedulerWorkflows(context.Context, domain.WorkflowID, int64) ([]domain.Workflow, error)
	ListSchedulerCandidates(context.Context, domain.WorkflowID, time.Time, int64, int64) ([]domain.WorkflowTask, error)
	GetWorkflow(context.Context, domain.WorkflowID) (domain.Workflow, bool, error)
	GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
	ListWorkflowAttempts(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowAttempt, error)
}

// CapabilityResolver 复用现有 capability.Resolver 的观测契约，静态配置不能授权。
type CapabilityResolver interface {
	Resolve(context.Context, ports.CapabilityProbeRequest) (domain.HarnessCapabilitySnapshot, error)
}

// Deps 保持真实服务边界；Claim 闭包允许 Workflow 注入本服务而不形成构造循环。
type Deps struct {
	Store          ports.SchedulerStore
	Tasks          CandidateStore
	Clock          func() time.Time
	OwnerID        string
	Resources      ports.ResourceSampler
	Capabilities   CapabilityResolver
	ResolveHarness func(context.Context, domain.WorkflowTask) (domain.AgentHarness, error)
	Claim          func(context.Context, workflow.ClaimInput) (workflow.ClaimResult, bool, error)
	Reservation    func(context.Context, domain.WorkflowTask, []domain.BudgetPolicy) (*int64, string, string, error)
}

// Service 将轮次串行化，但不在 Claim 回调期间持有资源锁或领导权锁。
// 任期只能由本实例获取，恢复完成必须由外部真实对账者确认。
type Service struct {
	deps       Deps
	runMu      sync.Mutex
	leaderMu   sync.Mutex
	fence      domain.SchedulerFence
	resourceMu sync.Mutex
	sampledAt  time.Time
	sample     domain.ResourceSample
	window     resourceWindow
	capacity   Capacity
}

var _ ports.WorkflowAdmissionProvider = (*Service)(nil)

// New 不抢占领导权、不采样、不启动后台循环。
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	return &Service{deps: deps}
}

// MaintainLeadership 以持久化 CAS 获取或每十秒续租；绝不自行标记恢复完成。
func (s *Service) MaintainLeadership(ctx context.Context) (domain.SchedulerLeader, error) {
	if s.deps.Store == nil || s.deps.OwnerID == "" {
		return domain.SchedulerLeader{}, domain.ErrSchedulerFence
	}
	now := s.deps.Clock().UTC()
	s.leaderMu.Lock()
	defer s.leaderMu.Unlock()
	l, err := s.deps.Store.GetSchedulerLeader(ctx)
	if err != nil {
		s.fence = domain.SchedulerFence{}
		return l, err
	}
	if !l.LeaseUntil.After(now) {
		var acquired bool
		l, acquired, err = s.deps.Store.AcquireSchedulerLeader(ctx, s.deps.OwnerID, now, now.Add(LeaderLease))
		if err != nil || !acquired {
			s.fence = domain.SchedulerFence{}
			return l, errors.Join(domain.ErrSchedulerFence, err)
		}
		s.fence = l.Fence
		return l, nil
	}
	if s.fence.Epoch == 0 || l.Fence != s.fence {
		s.fence = domain.SchedulerFence{}
		return l, domain.ErrSchedulerFence
	}
	if l.LeaseUntil.Sub(now) <= LeaderLease-LeaderRenewal {
		for retry := 0; retry < 2; retry++ {
			renewedLeader, renewed, renewErr := s.deps.Store.RenewSchedulerLeader(ctx, l, now, now.Add(LeaderLease))
			if renewErr == nil && renewed {
				return renewedLeader, nil
			}
			// 本实例推进公平游标也会更新 revision；只有任期丢失才清除身份。
			current, readErr := s.deps.Store.GetSchedulerLeader(ctx)
			if readErr != nil || current.Fence != s.fence || !current.LeaseUntil.After(s.deps.Clock().UTC()) {
				s.fence = domain.SchedulerFence{}
				return current, errors.Join(domain.ErrSchedulerFence, renewErr, readErr)
			}
			l = current
			if renewErr != nil && !errors.Is(renewErr, domain.ErrSchedulerFence) {
				return l, renewErr
			}
		}
	}
	return l, nil
}

// currentLeader 每次从数据库验证本实例任期，旧 epoch 和未恢复状态都闭锁。
func (s *Service) currentLeader(ctx context.Context) (domain.SchedulerLeader, error) {
	s.leaderMu.Lock()
	fence := s.fence
	s.leaderMu.Unlock()
	if s.deps.Store == nil || fence.Epoch == 0 {
		return domain.SchedulerLeader{}, domain.ErrSchedulerFence
	}
	l, err := s.deps.Store.GetSchedulerLeader(ctx)
	if err != nil {
		return l, err
	}
	if l.Fence != fence || !l.Recovered || !l.LeaseUntil.After(s.deps.Clock().UTC()) {
		return l, domain.ErrSchedulerFence
	}
	return l, nil
}

// CurrentFence 只返回本实例持有且已完成恢复的任期，旧 daemon 不能借用数据库中新 Leader 的身份。
func (s *Service) CurrentFence(ctx context.Context) (domain.SchedulerFence, error) {
	leader, err := s.currentLeader(ctx)
	return leader.Fence, err
}

// PrepareWorkflowAdmission 为自动与旧 HTTP Claim 提供同一组可信证据，最终裁决在 Store 事务内。
func (s *Service) PrepareWorkflowAdmission(ctx context.Context, task domain.WorkflowTask) (domain.AdmissionEvidence, error) {
	if s.deps.Tasks == nil {
		return domain.AdmissionEvidence{}, domain.ErrAdmissionRequired
	}
	w, found, err := s.deps.Tasks.GetWorkflow(ctx, task.WorkflowID)
	if err != nil {
		return domain.AdmissionEvidence{}, err
	}
	if !found || w.State != domain.WorkflowStateActive || task.State != domain.WorkflowTaskStateReady {
		return domain.AdmissionEvidence{}, domain.ErrWorkflowTaskNotClaimable
	}
	h, err := s.harness(ctx, task)
	if err != nil {
		return domain.AdmissionEvidence{}, err
	}
	if err := s.eligibility(ctx, task, h); err != nil {
		return domain.AdmissionEvidence{}, err
	}
	e, budgets, err := s.prepareLaunchAdmission(ctx, h)
	if err != nil {
		return e, err
	}
	if s.deps.Reservation != nil {
		e.ReservationNanos, e.ReservationBasis, e.ReservationRef, err = s.deps.Reservation(ctx, task, budgets)
		if err != nil {
			return e, err
		}
		if e.ReservationNanos != nil && (*e.ReservationNanos < 0 || !domain.ValidReservationBasis(e.ReservationBasis, e.ReservationRef)) ||
			e.ReservationNanos == nil && (e.ReservationBasis != "" || e.ReservationRef != "") {
			return e, domain.ErrBudgetUnknown
		}
	}
	if _, err := s.currentLeader(ctx); err != nil {
		return e, err
	}
	e.Now = s.deps.Clock().UTC()
	return e, nil
}

// PrepareSessionAdmission 为所有普通启动提供与 Workflow 相同的领导权、资源与能力证据。
func (s *Service) PrepareSessionAdmission(ctx context.Context, cfg ports.SpawnConfig) (domain.AdmissionEvidence, error) {
	if (!cfg.Harness.IsKnown() && cfg.Harness != domain.HarnessFake) ||
		(cfg.RequestedMode != domain.SessionModeChat && cfg.RequestedMode != domain.SessionModeTUI) {
		return domain.AdmissionEvidence{}, domain.ErrCapabilityEvidenceInsufficient
	}
	e, _, err := s.prepareLaunchAdmission(ctx, cfg.Harness)
	return e, err
}

// prepareLaunchAdmission 不执行副作用；探测后重新校验当前任期和样本时间，慢探测不能放宽准入。
func (s *Service) prepareLaunchAdmission(ctx context.Context, h domain.AgentHarness) (domain.AdmissionEvidence, []domain.BudgetPolicy, error) {
	l, err := s.currentLeader(ctx)
	if err != nil {
		return domain.AdmissionEvidence{}, nil, err
	}
	e := domain.AdmissionEvidence{Fence: l.Fence, Now: s.deps.Clock().UTC()}
	global, revisions, budgets, err := s.policies(ctx, h)
	if err != nil {
		return e, nil, err
	}
	e.ExpectedPolicyRevisions = revisions
	if err := s.RefreshResources(ctx); err != nil {
		return e, nil, err
	}
	if s.deps.Capabilities == nil {
		return e, nil, domain.ErrCapabilityEvidenceInsufficient
	}
	c, err := s.deps.Capabilities.Resolve(ctx, ports.CapabilityProbeRequest{Harness: h})
	if err != nil {
		return e, nil, errors.Join(domain.ErrCapabilityEvidenceInsufficient, err)
	}
	e.Now = s.deps.Clock().UTC()
	if c.Validate() != nil || c.Harness != h || !c.Source.AllowsSessionGating() || c.SessionScoped() ||
		c.Readiness != domain.CapabilityReady || c.Expired(e.Now) || c.ObservedAt.After(e.Now) ||
		(c.AuthState != domain.AgentAuthenticationAuthorized && c.AuthState != domain.AgentAuthenticationNotApplicable) {
		return e, nil, domain.ErrCapabilityEvidenceInsufficient
	}
	e.Capability = c
	if _, err := s.currentLeader(ctx); err != nil {
		return e, nil, err
	}
	s.resourceMu.Lock()
	e.Resources = s.sample
	s.capacity = s.window.evaluate(s.sample, e.Now, global)
	e.EffectiveGlobalCap = s.capacity.Limit
	s.resourceMu.Unlock()
	if e.EffectiveGlobalCap == 0 {
		return e, nil, domain.ErrSchedulerCapacity
	}
	return e, budgets, nil
}

// harness 仅接受任务显式 Harness 或项目配置拥有者返回的可信选择。
func (s *Service) harness(ctx context.Context, task domain.WorkflowTask) (domain.AgentHarness, error) {
	h := task.AgentHarness
	if h == "" && s.deps.ResolveHarness != nil {
		var err error
		h, err = s.deps.ResolveHarness(ctx, task)
		if err != nil {
			return "", err
		}
	}
	if !h.IsKnown() && h != domain.HarnessFake {
		return "", domain.ErrCapabilityEvidenceInsufficient
	}
	return h, nil
}

// policies 读取全部版本；预算选额与余额算法留在预算服务及原子 Store。
func (s *Service) policies(ctx context.Context, h domain.AgentHarness) (domain.SchedulerPolicy, map[string]int64, []domain.BudgetPolicy, error) {
	global := domain.DefaultSchedulerPolicy("")
	adapter := domain.DefaultSchedulerPolicy(h)
	revisions := map[string]int64{global.ID: global.Revision, adapter.ID: adapter.Revision}
	policies, err := s.deps.Store.ListSchedulerPolicies(ctx)
	if err != nil {
		return global, nil, nil, err
	}
	for _, p := range policies {
		if err := p.Validate(); err != nil {
			return global, nil, nil, err
		}
		revisions[p.ID] = p.Revision
		if p.Scope == "global" {
			global = p
		}
	}
	budgets, err := s.deps.Store.ListBudgetPolicies(ctx)
	if err != nil {
		return global, nil, nil, err
	}
	for _, p := range budgets {
		if err := p.Validate(); err != nil {
			return global, nil, nil, err
		}
		revisions[p.RevisionKey()] = p.Revision
	}
	return global, revisions, budgets, nil
}
