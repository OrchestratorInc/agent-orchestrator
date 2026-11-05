package workflow

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// materializationStore 将原件重读、图检查及唯一物化结果放在同一持久事务中。
type materializationStore interface {
	MaterializeWorkflowTaskRequest(context.Context, domain.TaskRequestMaterializationInput, domain.WorkflowGraphLimits) (domain.TaskRequestMaterialization, bool, error)
}

// MaterializeTaskRequest 只接受本次独立审批派生的 Operator，不能复用原 Decide 身份或 Agent 申请。
// HTTP 必须先消费绑定本次请求的审批；ActorRef 只是由该身份生成的审计值。
func (s *Service) MaterializeTaskRequest(ctx context.Context, input domain.TaskRequestMaterializationInput, caller principal.Caller) (domain.TaskRequestMaterialization, bool, error) {
	if !caller.IsOperator() || caller.AttemptID != "" || caller.LeaseProven || caller.OperatorTokenFingerprint == "" {
		return domain.TaskRequestMaterialization{}, false, apierr.Forbidden("COLLAB_NOT_AUTHORIZED", "A new operator approval is required to materialize a task request")
	}
	store, ok := s.store.(materializationStore)
	if !ok {
		return domain.TaskRequestMaterialization{}, false, apierr.Internal("WORKFLOW_STORE_UNAVAILABLE", "Task materialization storage is unavailable")
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return domain.TaskRequestMaterialization{}, false, err
	}
	digest, shaErr := hex.DecodeString(input.RequestSHA)
	if strings.TrimSpace(string(input.WorkflowID)) == "" || strings.TrimSpace(input.RequestID) == "" ||
		input.ExpectedRequestRevision < 1 || input.ExpectedWorkflowRevision < 1 || shaErr != nil || len(digest) != 32 ||
		len(input.RequestFingerprint) != 64 || !domain.ValidSchedulerReason(input.Reason) {
		return domain.TaskRequestMaterialization{}, false, apierr.Invalid("INVALID_TASK_MATERIALIZATION", "Task request identity, revisions, fingerprint and reason are required", nil)
	}
	if s.graphLimits.MaxTasks < 1 || s.graphLimits.MaxDepth < 1 || s.graphLimits.MaxFanOut < 0 {
		return domain.TaskRequestMaterialization{}, false, apierr.Internal("WORKFLOW_LIMITS_NOT_CONFIGURED", "Workflow graph limits are not configured")
	}
	input.ActorRef = caller.Ref() + ":" + caller.OperatorTokenFingerprint
	input.Now = s.clock().UTC()
	result, created, err := store.MaterializeWorkflowTaskRequest(ctx, input, s.graphLimits)
	if err == nil {
		return result, created, nil
	}
	var graphErr *domain.WorkflowGraphValidationError
	if errors.As(err, &graphErr) || errors.Is(err, domain.ErrTaskMaterializationScope) ||
		errors.Is(err, domain.ErrTaskMaterializationUnsupported) || errors.Is(err, domain.ErrInvalidWorkflowTaskRequestPayload) {
		failure := apierr.Invalid("TASK_MATERIALIZATION_REJECTED", "The approved request cannot create a task within the current scope", nil)
		failure.Cause = err
		return domain.TaskRequestMaterialization{}, false, failure
	}
	if errors.Is(err, domain.ErrWorkflowTaskRequestConflict) || errors.Is(err, domain.ErrWorkflowTaskRequestGraphMoved) ||
		errors.Is(err, domain.ErrWorkflowTaskRequestTampered) || errors.Is(err, domain.ErrInvalidWorkflowTaskRequestDecision) {
		failure := apierr.Conflict("TASK_MATERIALIZATION_CONFLICT", "The approved request or workflow has changed", nil)
		failure.Cause = err
		return domain.TaskRequestMaterialization{}, false, failure
	}
	return domain.TaskRequestMaterialization{}, false, mapStoreError("TASK_MATERIALIZATION_FAILED", "The task request could not be materialized", err)
}
