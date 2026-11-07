package daemon

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/resources"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/budget"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// m3Services 在发布给 HTTP 和后台任务前完成循环依赖装配；构造期间不启动 goroutine。
type m3Services struct {
	m2Services
	Scheduler     *scheduler.Service
	Workflows     *workflow.Service
	Budget        *budget.Service
	store         *sqlite.Store
	planRetirer   workflowSessionRetirer
	planArtifacts *artifact.Service
}

// newM3Services 将所有启动路径绑定到本实例 Leader；闭包仅在装配结束后被调用。
func newM3Services(deps m2GatewayDeps) *m3Services {
	m := &m3Services{store: deps.Store}
	m.planRetirer, _ = deps.Sessions.(workflowSessionRetirer)
	deps.ExecutionFence = func(ctx context.Context) (domain.SchedulerFence, error) {
		return m.Scheduler.CurrentFence(ctx)
	}
	m.m2Services = newM2ServicesWith(deps)
	m.Scheduler = scheduler.New(scheduler.Deps{
		Store: deps.Store, Tasks: deps.Store, OwnerID: uuid.NewString(),
		Resources: resources.New(), Capabilities: m.Capabilities,
		ResolveHarness: m.resolveHarness,
		Claim: func(ctx context.Context, in workflow.ClaimInput) (workflow.ClaimResult, bool, error) {
			return m.Workflows.Claim(ctx, in)
		},
	})
	m.Workflows = newWorkflowServiceWithAdmission(deps.Store, m.Scheduler)
	m.Budget = budget.New(deps.Store, time.Now)
	return m
}

// resolveHarness 只在 Claim 前读取首个作用域仓库的 Worker 配置，Claim 后使用持久化 Admission。
func (m *m3Services) resolveHarness(ctx context.Context, task domain.WorkflowTask) (domain.AgentHarness, error) {
	repos, err := m.store.ListWorkflowRepositories(ctx, task.WorkflowID)
	if err != nil {
		return "", err
	}
	scoped, err := m.store.ListWorkflowTaskRepositories(ctx, task.WorkflowID, task.ID)
	if err != nil {
		return "", err
	}
	for _, repo := range repos {
		if len(scoped) > 0 && repo.RepoKey != scoped[0].RepoKey {
			continue
		}
		project, found, err := m.store.GetProject(ctx, string(repo.ProjectID))
		if err != nil {
			return "", err
		}
		if !found {
			break
		}
		return domain.AgentHarness(project.Config.Worker.Harness), nil
	}
	return "", domain.ErrCapabilityEvidenceInsufficient
}

// dispatch 只消费本次 Claim 的原任期，已有或不确定的执行不会在恢复中再次 Spawn。
func (m *m3Services) dispatch(ctx context.Context, d scheduler.Dispatch) error {
	permit, err := m.store.LoadExecutionPermit(ctx, d.AttemptID, d.Fence, time.Now().UTC())
	if err != nil {
		return err
	}
	ownership, err := execution.ResolveAttemptOwnership(ctx, m.store, d.WorkflowID, d.AttemptID)
	if err != nil {
		return err
	}
	if ownership.Task.ID != d.TaskID {
		return domain.ErrSchedulerAdmissionConflict
	}
	refs := make([]ports.WorkflowRepoKeyRef, 0, len(ownership.Repos))
	for _, repo := range ownership.Repos {
		refs = append(refs, ports.WorkflowRepoKeyRef{RepoKey: repo.RepoKey, ProjectID: repo.ProjectID, BaseSHA: repo.ExpectedBaseSHA})
	}
	_, err = m.Binder.BindAttempt(ports.WithExecutionPermit(ctx, permit), ports.SpawnRequest{
		WorkflowID: d.WorkflowID, TaskID: d.TaskID, AttemptID: d.AttemptID,
		ProjectID: ownership.ProjectID, Harness: ownership.Harness, Mode: domain.SessionModeChat,
		Model: ownership.Task.Model, Effort: ownership.Task.Effort, Profile: ownership.Task.Profile,
		SpawnKey: execution.SpawnKey(d.AttemptID, execution.ActionSpawn),
		Prompt:   ownership.Task.Title + "\n\n" + ownership.Task.Brief, Repos: refs,
	}, ownership.Repos)
	return err
}

// scheduleRetry 只为本次人工批准的失败任务记录退避；实际 READY 转换仍由 Workflow 的幂等边界执行。
func (m *m3Services) scheduleRetry(ctx context.Context, workflowID domain.WorkflowID, taskID domain.WorkflowTaskID, expectedRevision int64) error {
	task, found, err := m.store.GetWorkflowTask(ctx, workflowID, taskID)
	if err != nil {
		return err
	}
	if !found || task.State != domain.WorkflowTaskStateRetryWait {
		return nil
	}
	if task.Revision != expectedRevision {
		return domain.ErrWorkflowRevisionConflict
	}
	attempts, err := m.store.ListWorkflowAttempts(ctx, workflowID, taskID)
	if err != nil {
		return err
	}
	var latest domain.WorkflowAttempt
	for _, attempt := range attempts {
		if attempt.AttemptNo > latest.AttemptNo {
			latest = attempt
		}
	}
	_, err = m.Scheduler.ScheduleVerifiedRetry(ctx, scheduler.VerifiedRetry{
		WorkflowID: workflowID, TaskID: taskID, AttemptID: latest.ID, ReasonCode: "SAFE_TO_RETRY",
	})
	return err
}

// startReadyAttempts 由实查 Controller 和工作区证明开始执行；完成结果仍要求可信业务结算，不能由进程退出推断。
func (m *m3Services) startReadyAttempts(ctx context.Context, fence domain.SchedulerFence) error {
	admissions, err := m.store.ListSchedulerAdmissions(ctx, true)
	if err != nil {
		return err
	}
	for _, admission := range admissions {
		if admission.Fence != fence || admission.State != domain.AdmissionActive || admission.AttemptID == "" || admission.SessionID == "" {
			continue
		}
		permit, err := m.store.LoadExecutionPermit(ctx, admission.AttemptID, fence, time.Now().UTC())
		if err != nil {
			return err
		}
		if permit.AttemptState != domain.WorkflowAttemptStateClaimed || !permit.LeaseUntil.After(time.Now().UTC()) {
			continue
		}
		_, _, live, _, err := m.Binder.LiveTarget(ctx, admission.AttemptID)
		if err != nil {
			return err
		}
		if !live {
			continue
		}
		if _, _, err := m.Workflows.Start(ports.WithExecutionPermit(ctx, permit), workflow.StartInput{
			WorkflowID: permit.WorkflowID, AttemptID: permit.AttemptID, LeaseToken: permit.LeaseToken,
			IdempotencyKey: "scheduler-start:" + string(permit.AttemptID),
		}); err != nil {
			return err
		}
	}
	return nil
}
