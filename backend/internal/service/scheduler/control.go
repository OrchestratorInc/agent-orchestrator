package scheduler

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Status 是管理侧安全快照，HTTP 只选取任期、容量和状态，不序列化内部 Owner 或凭据。
type Status struct {
	Leader    domain.SchedulerLeader
	Resources domain.ResourceSample
	Capacity  Capacity
	Policies  []domain.SchedulerPolicy
	Occupied  int64
	ByHarness map[domain.AgentHarness]int64
}

// Status 读取既有采样并标识失效，不在查询中启动 Provider 或改变 Leader。
func (s *Service) Status(ctx context.Context) (Status, error) {
	out := Status{ByHarness: make(map[domain.AgentHarness]int64)}
	var err error
	out.Leader, err = s.deps.Store.GetSchedulerLeader(ctx)
	if err != nil {
		return out, err
	}
	out.Policies, err = s.deps.Store.ListSchedulerPolicies(ctx)
	if err != nil {
		return out, err
	}
	configured := make(map[string]bool, len(out.Policies))
	global := domain.DefaultSchedulerPolicy("")
	for _, p := range out.Policies {
		configured[p.ID] = true
		if p.Scope == "global" {
			global = p
		}
	}
	// 返回实际生效的默认值，使未配置过的代理也能在同一入口修改并发。
	for _, h := range domain.AllHarnesses {
		p := domain.DefaultSchedulerPolicy(h)
		if !configured[p.ID] {
			out.Policies = append(out.Policies, p)
		}
	}
	occupancy, err := s.deps.Store.ListSchedulerOccupancy(ctx)
	if err != nil {
		return out, err
	}
	out.Occupied = int64(len(occupancy))
	for _, h := range occupancy {
		out.ByHarness[h]++
	}
	s.resourceMu.Lock()
	out.Resources, out.Capacity = s.sample, s.capacity
	s.resourceMu.Unlock()
	now := s.deps.Clock().UTC()
	if global.ResourceLimitsDisabled {
		out.Capacity = Capacity{Limit: global.MaxConcurrent, MaxNewClaims: ClaimLimit, ReasonCode: "RESOURCE_LIMITS_DISABLED"}
	} else if out.Resources.ObservedAt.IsZero() || out.Resources.ObservedAt.After(now) || now.Sub(out.Resources.ObservedAt) > 3*SampleInterval {
		out.Capacity = Capacity{ReasonCode: "RESOURCE_UNKNOWN"}
	}
	if !out.Leader.Recovered || !out.Leader.LeaseUntil.After(now) {
		out.Capacity = Capacity{ReasonCode: "SCHEDULER_RECOVERY_REQUIRED"}
	}
	return out, nil
}

// WorkflowStatus 返回持久公平/暂停状态；不存在的工作流不得表现为默认可调度。
func (s *Service) WorkflowStatus(ctx context.Context, id domain.WorkflowID) (domain.SchedulerWorkflowState, error) {
	w, found, err := s.deps.Tasks.GetWorkflow(ctx, id)
	if err != nil {
		return domain.SchedulerWorkflowState{}, err
	}
	if !found {
		return domain.SchedulerWorkflowState{}, apierr.NotFound("WORKFLOW_NOT_FOUND", "Unknown workflow")
	}
	states, err := s.deps.Store.ListSchedulerWorkflowStates(ctx)
	if err != nil {
		return domain.SchedulerWorkflowState{}, err
	}
	for _, state := range states {
		if state.WorkflowID == id {
			return state, nil
		}
	}
	return domain.SchedulerWorkflowState{WorkflowID: w.ID, Weight: 1}, nil
}

// PutPolicy 再检查本次 Operator 身份并覆盖客户端不可指定的审计主体和时间。
func (s *Service) PutPolicy(ctx context.Context, p domain.SchedulerPolicy, expected int64, change domain.PolicyChange, caller principal.Caller) (domain.SchedulerPolicy, bool, error) {
	if !schedulerOperator(caller) {
		return domain.SchedulerPolicy{}, false, principal.ErrNotAuthorized
	}
	change.ActorRef, change.Now = caller.OperatorTokenFingerprint, s.deps.Clock().UTC()
	if p.Scope == "global" {
		s.resourceMu.Lock()
		defer s.resourceMu.Unlock()
	}
	p, applied, err := s.deps.Store.PutSchedulerPolicy(ctx, p, expected, change)
	if err == nil && applied && p.Scope == "global" {
		// 用户改变策略后立即重算容量，旧策略留下的降额不能继续锁住新策略。
		s.window = resourceWindow{}
		s.capacity = s.window.evaluate(s.sample, s.deps.Clock().UTC(), p)
	}
	return p, applied, err
}

// SetWorkflowPaused 只控制新增调度，不终止既有进程，也不绕过恢复或硬预算。
func (s *Service) SetWorkflowPaused(ctx context.Context, id domain.WorkflowID, paused bool, expected int64, change domain.PolicyChange, caller principal.Caller) (domain.SchedulerWorkflowState, bool, error) {
	if !schedulerOperator(caller) {
		return domain.SchedulerWorkflowState{}, false, principal.ErrNotAuthorized
	}
	change.ActorRef, change.Now = caller.OperatorTokenFingerprint, s.deps.Clock().UTC()
	return s.deps.Store.SetWorkflowScheduling(ctx, id, paused, expected, change)
}

// Decisions 只提供有界、安全原因审计，完整审批正文不进入响应。
func (s *Service) Decisions(ctx context.Context, after, limit int64) ([]domain.SchedulerDecision, error) {
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, domain.ErrSchedulerPolicy
	}
	return s.deps.Store.ListSchedulerDecisions(ctx, after, limit)
}

func schedulerOperator(c principal.Caller) bool {
	return c.IsOperator() && !c.IsAgent() && !c.LeaseProven && c.AttemptID == "" && c.SessionID == "" && strings.TrimSpace(c.OperatorTokenFingerprint) != ""
}
