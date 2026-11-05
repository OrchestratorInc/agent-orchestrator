package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ResourceSampler 提供带时间与原始 CPU tick 的可信本机资源证据。
type ResourceSampler interface {
	Sample(context.Context) (domain.ResourceSample, error)
}

// WorkflowAdmissionProvider 由 daemon 内部组合资源、能力和策略证据；不接受外部余额。
type WorkflowAdmissionProvider interface {
	PrepareWorkflowAdmission(context.Context, domain.WorkflowTask) (domain.AdmissionEvidence, error)
}

// SchedulerStore 统一跨进程 Leader、资源、预算和幂等领取的持久边界。
type SchedulerStore interface {
	AcquireSchedulerLeader(context.Context, string, time.Time, time.Time) (domain.SchedulerLeader, bool, error)
	RenewSchedulerLeader(context.Context, domain.SchedulerLeader, time.Time, time.Time) (domain.SchedulerLeader, bool, error)
	CompleteSchedulerRecovery(context.Context, domain.SchedulerFence, int64, time.Time) (domain.SchedulerLeader, bool, error)
	GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error)
	ReserveAndClaim(context.Context, domain.ClaimAdmissionInput) (domain.WorkflowAttempt, domain.SchedulerAdmission, bool, error)
	ReserveSessionAdmission(context.Context, domain.SessionAdmissionInput) (domain.SchedulerAdmission, bool, error)
	FindWorkflowClaimAdmission(context.Context, domain.WorkflowID, string, string) (domain.WorkflowAttempt, domain.SchedulerAdmission, bool, error)
	GetSchedulerAdmission(context.Context, string) (domain.SchedulerAdmission, bool, error)
	AdoptSchedulerAdmission(context.Context, string, int64, domain.SchedulerFence, time.Time) (domain.SchedulerAdmission, bool, error)
	ListSchedulerAdmissions(context.Context, bool) ([]domain.SchedulerAdmission, error)
	ListSchedulerOccupancy(context.Context) ([]domain.AgentHarness, error)
	SetWorkflowScheduling(context.Context, domain.WorkflowID, bool, int64, domain.PolicyChange) (domain.SchedulerWorkflowState, bool, error)
	ListSchedulerPolicies(context.Context) ([]domain.SchedulerPolicy, error)
	PutSchedulerPolicy(context.Context, domain.SchedulerPolicy, int64, domain.PolicyChange) (domain.SchedulerPolicy, bool, error)
	ListBudgetPolicies(context.Context) ([]domain.BudgetPolicy, error)
	PutBudgetPolicy(context.Context, domain.BudgetPolicy, int64, domain.PolicyChange) (domain.BudgetPolicy, bool, error)
	ListBudgetReservations(context.Context, string) ([]domain.BudgetReservation, error)
	ListSchedulerDecisions(context.Context, int64, int64) ([]domain.SchedulerDecision, error)
	RecordSchedulerDecision(context.Context, domain.SchedulerDecision) error
	PutSchedulerWorkflowState(context.Context, domain.SchedulerWorkflowState, int64, domain.SchedulerFence, time.Time) (bool, error)
	ListSchedulerWorkflowStates(context.Context) ([]domain.SchedulerWorkflowState, error)
	SetSchedulerCursor(context.Context, domain.SchedulerFence, int64, string, time.Time) (bool, error)
	PutSchedulerRetryDue(context.Context, domain.SchedulerRetryDue, domain.SchedulerFence, time.Time) error
	ListSchedulerRetryDue(context.Context) ([]domain.SchedulerRetryDue, error)
}
