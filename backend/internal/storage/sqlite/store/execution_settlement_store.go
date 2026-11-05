package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// replayExecutionSettlement 仅回放同一已结算请求；终态租约已消费，不能再用 live 门禁。
// 当前内部许可、任期和原请求指纹仍必须一致，故回放不授予任何新的执行或写权限。
func replayExecutionSettlement(ctx context.Context, q *gen.Queries, workflowID domain.WorkflowID, attemptID domain.WorkflowAttemptID, state domain.WorkflowAttemptState, code, key, fingerprint string, now time.Time, out *domain.WorkflowAttempt) (bool, error) {
	want, ok := ports.ExecutionPermitFromContext(ctx)
	if !ok || want.AttemptID != attemptID || want.WorkflowID != workflowID {
		return false, domain.ErrAdmissionRequired
	}
	got, err := loadExecutionPermit(ctx, q, attemptID)
	if err != nil {
		return false, err
	}
	if got != want {
		return false, domain.ErrSchedulerFence
	}
	if err := schedulerFence(ctx, q, want.Fence, now, false); err != nil {
		return false, err
	}
	if got.AttemptState != domain.WorkflowAttemptStateSucceeded && got.AttemptState != domain.WorkflowAttemptStateFailed {
		return false, nil
	}
	replay, found, err := lookupWorkflowIdempotency(ctx, q, key, workflowID, domain.WorkflowOperationSettleAttempt, fingerprint, "attempt")
	if err != nil {
		return false, err
	}
	if found && replay.ResultID != string(attemptID) {
		return false, domain.ErrWorkflowIdempotencyConflict
	}
	recorded, err := q.HasExecutionSettlementReplay(ctx, gen.HasExecutionSettlementReplayParams{WorkflowID: workflowID, RequestFingerprint: fingerprint, AttemptID: string(attemptID)})
	if err != nil {
		return false, err
	}
	row, err := q.GetWorkflowAttempt(ctx, attemptID)
	if err != nil {
		return false, err
	}
	if !recorded || row.State != state || row.ErrorCode != code {
		return false, domain.ErrWorkflowAttemptLeaseConflict
	}
	*out = workflowAttemptFromGen(row)
	return true, nil
}
