package sqlitetest

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AdmissionFixture 显式提供隔离 SQLite 的可信假证据；不改变 Open 默认值或释放占位。
type AdmissionStore interface {
	ports.SchedulerStore
	GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error)
	GetWorkflow(context.Context, domain.WorkflowID) (domain.Workflow, bool, error)
}

type AdmissionFixture struct {
	Store AdmissionStore
	Clock func() time.Time
}

// NewAdmissionFixture 使用共享数据库中同一测试 Leader，保留生产默认容量与所有占位。
func NewAdmissionFixture(t testing.TB, s AdmissionStore, clock func() time.Time) *AdmissionFixture {
	t.Helper()
	if clock == nil {
		t.Fatal("admission fixture requires an explicit clock")
	}
	f := &AdmissionFixture{Store: s, Clock: clock}
	if _, err := f.leader(context.Background(), clock()); err != nil {
		t.Fatalf("initialize explicit admission fixture: %v", err)
	}
	return f
}

// PrepareSessionAdmission 为 Manager 的真实 Store 夹具显式提供假能力与资源，仍由原子 Store 限制容量。
func (f *AdmissionFixture) PrepareSessionAdmission(ctx context.Context, cfg ports.SpawnConfig) (domain.AdmissionEvidence, error) {
	return f.PrepareWorkflowAdmission(ctx, domain.WorkflowTask{AgentHarness: cfg.Harness})
}

// CurrentFence 为仅观察的重附着提供测试任期，不伪造资源或预算准入。
func (f *AdmissionFixture) CurrentFence(ctx context.Context) (domain.SchedulerFence, error) {
	l, err := f.leader(ctx, f.Clock())
	return l.Fence, err
}

// leader 只接管真正到期的测试租约；重复构造不会重置共享任期。
func (f *AdmissionFixture) leader(ctx context.Context, now time.Time) (domain.SchedulerLeader, error) {
	l, err := f.Store.GetSchedulerLeader(ctx)
	if err != nil {
		return l, err
	}
	if !l.LeaseUntil.After(now) {
		l, _, err = f.Store.AcquireSchedulerLeader(ctx, "sqlitetest-admission", now, now.Add(30*time.Second))
		if err != nil {
			return l, err
		}
	}
	if l.Fence.OwnerID != "sqlitetest-admission" {
		return l, domain.ErrSchedulerFence
	}
	if !l.Recovered {
		expectedFence := l.Fence
		l, _, err = f.Store.CompleteSchedulerRecovery(ctx, l.Fence, l.Revision, now)
		if err != nil {
			current, readErr := f.Store.GetSchedulerLeader(ctx)
			if readErr == nil && current.Fence == expectedFence && current.Recovered {
				return current, nil
			}
			return l, err
		}
	}
	return l, nil
}

// PrepareWorkflowAdmission 生成当前假资源与已确认能力，并读取所有策略 revision。
func (f *AdmissionFixture) PrepareWorkflowAdmission(ctx context.Context, task domain.WorkflowTask) (domain.AdmissionEvidence, error) {
	now := f.Clock().UTC()
	l, err := f.leader(ctx, now)
	if err != nil {
		return domain.AdmissionEvidence{}, err
	}
	h := task.AgentHarness
	if h == "" {
		h = domain.HarnessCodex
	}
	c := domain.NewHarnessCapabilitySnapshot(h, domain.CapabilitySourceConfirmedRuntime, now, time.Minute)
	c.Readiness = domain.CapabilityReady
	c.AuthState = domain.AgentAuthenticationAuthorized
	c.Surfaces = domain.CapabilitySurfaceBoth
	e := domain.AdmissionEvidence{Fence: l.Fence, Now: now, Capability: c, EffectiveGlobalCap: domain.SchedulerDefaultGlobalCap, ExpectedPolicyRevisions: map[string]int64{"adapter:" + string(h): 0}, Resources: domain.ResourceSample{ObservedAt: now, PreviousAt: now.Add(-5 * time.Second), TotalBytes: 16 << 30, AvailableBytes: 12 << 30, CPUTotalTicks: 200, CPUBusyTicks: 20, PreviousCPUTotalTicks: 100, PreviousCPUBusyTicks: 10}}
	policies, err := f.Store.ListSchedulerPolicies(ctx)
	if err != nil {
		return e, err
	}
	for _, p := range policies {
		e.ExpectedPolicyRevisions[p.ID] = p.Revision
		if p.Scope == "global" {
			e.EffectiveGlobalCap = p.MaxConcurrent
		}
	}
	budgets, err := f.Store.ListBudgetPolicies(ctx)
	if err != nil {
		return e, err
	}
	for _, p := range budgets {
		e.ExpectedPolicyRevisions[p.RevisionKey()] = p.Revision
	}
	return e, nil
}

// Claim 保留旧测试的输入与错误语义，只将领取改走真实准入事务。
func (f *AdmissionFixture) Claim(ctx context.Context, a domain.WorkflowAttempt, revision int64, key, fp string) (domain.WorkflowAttempt, bool, error) {
	if replay, _, found, err := f.Store.FindWorkflowClaimAdmission(ctx, a.WorkflowID, key, fp); err != nil || found {
		return replay, false, err
	}
	t, _, err := f.Store.GetWorkflowTask(ctx, a.WorkflowID, a.TaskID)
	if err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	e, err := f.PrepareWorkflowAdmission(ctx, t)
	if err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	w, _, err := f.Store.GetWorkflow(ctx, a.WorkflowID)
	if err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	result, _, created, err := f.Store.ReserveAndClaim(ctx, domain.ClaimAdmissionInput{Attempt: a, ExpectedTaskRevision: revision, ExpectedWorkflowRevision: w.Revision, IdempotencyKey: key, RequestFingerprint: fp, Evidence: e})
	return result, created, err
}
