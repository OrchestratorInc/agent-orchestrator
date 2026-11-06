package scheduler

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// Dispatch 只携带已授予的 Attempt 身份；执行器随后从持久化事实取得绑定及租约。
type Dispatch struct {
	WorkflowID domain.WorkflowID
	TaskID     domain.WorkflowTaskID
	AttemptID  domain.WorkflowAttemptID
	Fence      domain.SchedulerFence
}

// TickResult 是有界一轮的非秘密结果，拒绝原因同时写入持久审计。
type TickResult struct {
	Dispatches []Dispatch
	Decisions  []domain.SchedulerDecision
	Examined   int
	Capacity   Capacity
}

// Tick 执行持久游标的 Weighted Deficit Round Robin；每个工作流最多取五个候选。
// 信用仅在领取成功时消耗，阻断工作流会推进游标；Store 失败则停止并保留已领取结果。
func (s *Service) Tick(ctx context.Context) (TickResult, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	out := TickResult{}
	l, err := s.currentLeader(ctx)
	if err != nil {
		return out, err
	}
	if s.deps.Tasks == nil || s.deps.Claim == nil {
		return out, domain.ErrAdmissionRequired
	}
	if err := s.RefreshResources(ctx); err != nil {
		return out, err
	}
	s.resourceMu.Lock()
	out.Capacity = s.capacity
	s.resourceMu.Unlock()
	if out.Capacity.Limit == 0 {
		return out, nil
	}
	states, err := s.deps.Store.ListSchedulerWorkflowStates(ctx)
	if err != nil {
		return out, err
	}
	byWorkflow := make(map[domain.WorkflowID]domain.SchedulerWorkflowState, len(states))
	for _, v := range states {
		if v.Weight < 1 || v.Weight > 5 || v.Deficit < 0 || v.CandidateOffset < 0 {
			return out, domain.ErrSchedulerPolicy
		}
		byWorkflow[v.WorkflowID] = v
	}
	workflows, err := s.workflowPage(ctx, domain.WorkflowID(l.Cursor))
	if err != nil {
		return out, err
	}
	for _, w := range workflows {
		if out.Examined == CandidateLimit || len(out.Dispatches) >= min(ClaimLimit, int(out.Capacity.MaxNewClaims)) {
			break
		}
		if _, err := s.currentLeader(ctx); err != nil {
			return out, err
		}
		v, exists := byWorkflow[w.ID]
		if !exists {
			v = domain.SchedulerWorkflowState{WorkflowID: w.ID, Weight: 1}
		}
		if !v.Paused {
			if v.Deficit == 0 {
				v.Deficit = v.Weight
			}
			v.Deficit = min(5, v.Deficit)
			batchLimit := min(5, CandidateLimit-out.Examined)
			candidates, err := s.deps.Tasks.ListSchedulerCandidates(ctx, w.ID, s.deps.Clock().UTC(), int64(batchLimit), v.CandidateOffset)
			if err != nil {
				return out, err
			}
			if len(candidates) > min(5, CandidateLimit-out.Examined) {
				return out, domain.ErrSchedulerPolicy
			}
			processed := 0
			readyChanged := false
			for _, task := range candidates {
				if v.Deficit == 0 || len(out.Dispatches) >= min(ClaimLimit, int(out.Capacity.MaxNewClaims)) {
					break
				}
				out.Examined++
				processed++
				key := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", w.ID, task.ID, task.Revision)))
				result, created, claimErr := s.deps.Claim(ctx, workflow.ClaimInput{WorkflowID: w.ID, TaskID: task.ID, ExpectedRevision: task.Revision,
					WorkerID: s.deps.OwnerID, IdempotencyKey: fmt.Sprintf("scheduler:%x", key)})
				if claimErr != nil {
					reason := refusalReason(claimErr)
					if reason == "" {
						return out, claimErr
					}
					if _, err := s.currentLeader(ctx); err != nil {
						return out, errors.Join(claimErr, err)
					}
					_, revisions, _, policyErr := s.policies(ctx, task.AgentHarness)
					if policyErr != nil {
						return out, errors.Join(claimErr, policyErr)
					}
					d := domain.SchedulerDecision{WorkflowID: w.ID, TaskID: task.ID, Fence: l.Fence, ReasonCode: reason, PolicyRevisions: revisions, CreatedAt: s.deps.Clock().UTC()}
					if err := s.deps.Store.RecordSchedulerDecision(ctx, d); err != nil {
						return out, errors.Join(claimErr, err)
					}
					out.Decisions = append(out.Decisions, d)
					current, found, err := s.deps.Tasks.GetWorkflowTask(ctx, w.ID, task.ID)
					if err != nil {
						return out, err
					}
					readyChanged = readyChanged || !found || current.State != domain.WorkflowTaskStateReady
				} else if created {
					v.Deficit--
					out.Dispatches = append(out.Dispatches, Dispatch{WorkflowID: w.ID, TaskID: task.ID, AttemptID: result.Attempt.ID, Fence: l.Fence})
				}
				readyChanged = readyChanged || claimErr == nil
				s.resourceMu.Lock()
				out.Capacity = s.capacity
				s.resourceMu.Unlock()
			}
			if len(candidates) == 0 {
				v.Deficit = 0
			}
			// READY 集合缩短时从头重排，已移除项不能继续增加 OFFSET 并跳过更高优先级任务。
			if readyChanged || len(candidates) < batchLimit || v.CandidateOffset > math.MaxInt64-int64(processed) {
				v.CandidateOffset = 0
			} else {
				v.CandidateOffset += int64(processed)
			}
			ok, err := s.deps.Store.PutSchedulerWorkflowState(ctx, v, v.Revision, l.Fence, s.deps.Clock().UTC())
			if err != nil || !ok {
				return out, errors.Join(domain.ErrSchedulerPolicy, err)
			}
			if v.Deficit > 0 && processed < len(candidates) {
				return out, nil
			}
		}
		// 先提交信用、后推进游标；若中途退出，幂等 Claim 不重复占位。
		l, err = s.currentLeader(ctx)
		if err != nil {
			return out, err
		}
		ok, err := s.deps.Store.SetSchedulerCursor(ctx, l.Fence, l.Revision, string(w.ID), s.deps.Clock().UTC())
		if err != nil || !ok {
			return out, errors.Join(domain.ErrSchedulerFence, err)
		}
	}
	return out, nil
}

// workflowPage 以 id keyset 读取至多一百个工作流，并在尾部绕回；频繁更新不改变顺序。
func (s *Service) workflowPage(ctx context.Context, cursor domain.WorkflowID) ([]domain.Workflow, error) {
	rows, err := s.deps.Tasks.ListSchedulerWorkflows(ctx, cursor, CandidateLimit)
	if err != nil {
		return nil, err
	}
	if cursor != "" && len(rows) < CandidateLimit {
		head, err := s.deps.Tasks.ListSchedulerWorkflows(ctx, "", int64(CandidateLimit-len(rows)))
		if err != nil {
			return nil, err
		}
		for _, w := range head {
			if w.ID > cursor {
				break
			}
			rows = append(rows, w)
		}
	}
	if len(rows) > CandidateLimit {
		return nil, domain.ErrSchedulerPolicy
	}
	return rows, nil
}

// eligibility 对旧 HTTP Claim 同样检查暂停、重试上限、已核验重试和持久化冷却。
func (s *Service) eligibility(ctx context.Context, task domain.WorkflowTask, h domain.AgentHarness) error {
	states, err := s.deps.Store.ListSchedulerWorkflowStates(ctx)
	if err != nil {
		return err
	}
	for _, v := range states {
		if v.WorkflowID == task.WorkflowID && v.Paused {
			return domain.ErrWorkflowTaskNotClaimable
		}
	}
	attempts, err := s.deps.Tasks.ListWorkflowAttempts(ctx, task.WorkflowID, task.ID)
	if err != nil {
		return err
	}
	if int64(len(attempts)) >= min(task.MaxAttempts, 3) {
		return domain.ErrWorkflowTaskNotClaimable
	}
	for _, a := range attempts {
		if a.State != domain.WorkflowAttemptStateFailed && a.State != domain.WorkflowAttemptStateSucceeded {
			return domain.ErrWorkflowTaskNotClaimable
		}
	}
	due, err := s.deps.Store.ListSchedulerRetryDue(ctx)
	if err != nil {
		return err
	}
	verified := len(attempts) == 0
	for _, d := range due {
		own := d.WorkflowID == task.WorkflowID && d.TaskID == task.ID
		if d.NextEligibleAt.After(s.deps.Clock().UTC()) && (own || d.ReasonCode == "RATE_LIMITED" && d.Harness == h) {
			return domain.ErrWorkflowTaskNotClaimable
		}
		if own && (d.ReasonCode == "SAFE_TO_RETRY" || d.ReasonCode == "RATE_LIMITED") && len(attempts) > 0 {
			sort.Slice(attempts, func(i, j int) bool { return attempts[i].AttemptNo > attempts[j].AttemptNo })
			last := attempts[0]
			verified = d.AttemptID == last.ID && last.State == domain.WorkflowAttemptStateFailed && last.EndedAt != nil && !d.NextEligibleAt.Before(last.EndedAt.Add(5*time.Second))
		}
	}
	if !verified {
		return domain.ErrWorkflowTaskNotClaimable
	}
	return nil
}

// refusalReason 只将可解释的任务门禁转换为审计；底层读取或未知错误必须返回调用者。
func refusalReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrSchedulerFence):
		return ""
	case errors.Is(err, domain.ErrSchedulerCapacity):
		return "CAPACITY_UNAVAILABLE"
	case errors.Is(err, domain.ErrPathReservationConflict):
		return "PATH_RESERVATION_CONFLICT"
	case errors.Is(err, domain.ErrDeliveryBlocked):
		return "QUALITY_GATE_BLOCKED"
	case errors.Is(err, domain.ErrCapabilityEvidenceInsufficient), errors.Is(err, domain.ErrInvalidCapabilityHarness):
		return "CAPABILITY_UNAVAILABLE"
	case errors.Is(err, domain.ErrBudgetExceeded):
		return "BUDGET_EXCEEDED"
	case errors.Is(err, domain.ErrBudgetUnknown):
		return "BUDGET_UNKNOWN"
	case errors.Is(err, domain.ErrWorkflowTaskNotClaimable), errors.Is(err, domain.ErrWorkflowRevisionConflict):
		return "TASK_NOT_ELIGIBLE"
	}
	var apiError *apierr.Error
	if errors.As(err, &apiError) && (apiError.Code == "WORKFLOW_TASK_NOT_CLAIMABLE" || apiError.Code == "WORKFLOW_REVISION_CONFLICT") {
		return "TASK_NOT_ELIGIBLE"
	}
	return ""
}
