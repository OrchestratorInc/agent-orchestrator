package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// VerifiedRetry 只能由已核实无重复副作用风险的执行恢复服务提交，不能从任意错误文案构造。
type VerifiedRetry struct {
	WorkflowID domain.WorkflowID
	TaskID     domain.WorkflowTaskID
	AttemptID  domain.WorkflowAttemptID
	ReasonCode string
	RetryAfter time.Time
}

// ScheduleVerifiedRetry 核对持久化最新失败，再记录有界退避；不修改 Task 状态或重放执行。
func (s *Service) ScheduleVerifiedRetry(ctx context.Context, in VerifiedRetry) (domain.SchedulerRetryDue, error) {
	l, err := s.currentLeader(ctx)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	if s.deps.Tasks == nil || (in.ReasonCode != "SAFE_TO_RETRY" && in.ReasonCode != "RATE_LIMITED") {
		return domain.SchedulerRetryDue{}, domain.ErrWorkflowTaskNotClaimable
	}
	task, found, err := s.deps.Tasks.GetWorkflowTask(ctx, in.WorkflowID, in.TaskID)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	if !found || task.State != domain.WorkflowTaskStateRetryWait {
		return domain.SchedulerRetryDue{}, domain.ErrWorkflowTaskNotClaimable
	}
	attempts, err := s.deps.Tasks.ListWorkflowAttempts(ctx, task.WorkflowID, task.ID)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	var latest domain.WorkflowAttempt
	for _, a := range attempts {
		if a.AttemptNo > latest.AttemptNo {
			latest = a
		}
	}
	if latest.ID != in.AttemptID || latest.State != domain.WorkflowAttemptStateFailed || latest.EndedAt == nil || latest.AttemptNo >= min(task.MaxAttempts, 3) {
		return domain.SchedulerRetryDue{}, domain.ErrWorkflowTaskNotClaimable
	}
	h, err := s.harness(ctx, task)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	p, _, _, err := s.policies(ctx, h)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	now := s.deps.Clock().UTC()
	start := now
	if latest.EndedAt.After(start) {
		start = *latest.EndedAt
	}
	next := start.Add(retryDelay(latest.AttemptNo, latest.ID))
	if in.RetryAfter.After(next) {
		next = in.RetryAfter.UTC()
	}
	due := domain.SchedulerRetryDue{WorkflowID: task.WorkflowID, TaskID: task.ID, AttemptID: latest.ID, Harness: h, NextEligibleAt: next,
		ReasonCode: in.ReasonCode, PolicyRevision: p.Revision}
	if err := s.deps.Store.PutSchedulerRetryDue(ctx, due, l.Fence, now); err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	persisted, err := s.deps.Store.ListSchedulerRetryDue(ctx)
	if err != nil {
		return domain.SchedulerRetryDue{}, err
	}
	for _, row := range persisted {
		if row.WorkflowID == due.WorkflowID && row.TaskID == due.TaskID && row.AttemptID == latest.ID {
			return row, nil
		}
	}
	return domain.SchedulerRetryDue{}, domain.ErrSchedulerAdmissionConflict
}

// retryDelay 使用 Attempt 身份产生可重放的非负 jitter，整个退避不超过五分钟。
func retryDelay(attemptNo int64, id domain.WorkflowAttemptID) time.Duration {
	delay := 5 * time.Second
	for n := int64(1); n < attemptNo && delay < 5*time.Minute; n++ {
		delay *= 2
	}
	delay = min(delay, 5*time.Minute)
	sum := sha256.Sum256([]byte(id))
	jitter := time.Duration(binary.BigEndian.Uint64(sum[:8]) % uint64(delay/5+1))
	return min(delay+jitter, 5*time.Minute)
}
