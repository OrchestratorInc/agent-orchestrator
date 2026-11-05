package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ActivateWorkflow compare-and-swaps a draft workflow to active and makes
// dependency-free pending tasks ready in the same transaction.
func (s *Store) ActivateWorkflow(
	ctx context.Context,
	workflowID domain.WorkflowID,
	expectedRevision int64,
	idempotencyKey string,
	requestFingerprint string,
	now time.Time,
) (domain.Workflow, bool, error) {
	if strings.TrimSpace(idempotencyKey) == "" || len(requestFingerprint) != 64 {
		return domain.Workflow{}, false, domain.ErrInvalidWorkflow
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.Workflow
	applied := false
	err := s.inTx(ctx, "activate workflow", func(q *gen.Queries) error {
		rows, err := q.ActivateWorkflow(ctx, gen.ActivateWorkflowParams{
			UpdatedAt: now, WorkflowID: workflowID, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			record, found, err := lookupWorkflowIdempotency(
				ctx, q, idempotencyKey, workflowID, domain.WorkflowOperationActivate,
				requestFingerprint, "workflow",
			)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowRevisionConflict
			}
			row, err := q.GetWorkflow(ctx, record.WorkflowID)
			if err != nil {
				return err
			}
			result = workflowFromGen(row)
			return nil
		}

		if _, err := q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{
			UpdatedAt: now, WorkflowID: workflowID,
		}); err != nil {
			return err
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: workflowID, Operation: domain.WorkflowOperationActivate,
			Fingerprint: requestFingerprint, ResultType: "workflow", ResultID: string(workflowID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		row, err := q.GetWorkflow(ctx, workflowID)
		if err != nil {
			return err
		}
		result, applied = workflowFromGen(row), true
		return nil
	})
	if errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		return s.loadIdempotentWorkflow(ctx, idempotencyKey, workflowID, domain.WorkflowOperationActivate, requestFingerprint)
	}
	if err != nil {
		return domain.Workflow{}, false, fmt.Errorf("activate workflow %s: %w", workflowID, err)
	}
	return result, applied, nil
}

// ClaimWorkflowTask 保留旧调用的参数错误语义，拒绝没有可信准入证据的新领取。
func (s *Store) ClaimWorkflowTask(ctx context.Context, attempt domain.WorkflowAttempt, expectedTaskRevision int64, key, fingerprint string) (domain.WorkflowAttempt, bool, error) {
	if err := validateAdmissionAttempt(attempt, key, fingerprint); err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	return domain.WorkflowAttempt{}, false, domain.ErrAdmissionRequired
}

// StartWorkflowAttempt changes a live claim to running and advances the task
// state with a revision fence. Expired leases cannot be started.
//
// Lease ordering is a TEXT comparison in SQLite, so every instant written here
// is normalized to UTC; mixed offsets would compare chronologically wrong.
func (s *Store) StartWorkflowAttempt(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	leaseToken string,
	idempotencyKey string,
	requestFingerprint string,
	now time.Time,
) (domain.WorkflowAttempt, bool, error) {
	if strings.TrimSpace(leaseToken) == "" || strings.TrimSpace(idempotencyKey) == "" ||
		len(requestFingerprint) != 64 {
		return domain.WorkflowAttempt{}, false, domain.ErrWorkflowAttemptLeaseConflict
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttempt
	applied := false
	err := s.inSchedulerTx(ctx, "start workflow attempt", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, attemptID, now, true); err != nil {
			return err
		}
		rows, err := q.StartWorkflowAttempt(ctx, gen.StartWorkflowAttemptParams{
			Now: timePtrToNullTime(&now), ID: attemptID, WorkflowID: workflowID, LeaseToken: leaseToken,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return classifyAttemptZeroRow(
				ctx, q, workflowID, attemptID, leaseToken, idempotencyKey, requestFingerprint,
				domain.WorkflowOperationStartAttempt, domain.WorkflowAttemptStateRunning, now, &result,
			)
		}

		attemptRow, err := q.GetWorkflowAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		taskRow, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{
			WorkflowID: workflowID, ID: attemptRow.TaskID,
		})
		if err != nil {
			return err
		}
		taskRows, err := q.UpdateWorkflowTaskState(ctx, gen.UpdateWorkflowTaskStateParams{
			NextState: domain.WorkflowTaskStateRunning, UpdatedAt: now,
			WorkflowID: workflowID, TaskID: attemptRow.TaskID,
			ExpectedState: domain.WorkflowTaskStateClaimed, ExpectedRevision: taskRow.Revision,
		})
		if err != nil {
			return err
		}
		if taskRows == 0 {
			return domain.ErrWorkflowRevisionConflict
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: workflowID, Operation: domain.WorkflowOperationStartAttempt,
			Fingerprint: requestFingerprint, ResultType: "attempt", ResultID: string(attemptID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		result, applied = workflowAttemptFromGen(attemptRow), true
		return nil
	})
	if errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		return s.loadIdempotentAttempt(
			ctx, idempotencyKey, workflowID, domain.WorkflowOperationStartAttempt, requestFingerprint,
		)
	}
	if err != nil {
		return domain.WorkflowAttempt{}, false, fmt.Errorf("start workflow attempt %s: %w", attemptID, err)
	}
	return result, applied, nil
}

// classifyAttemptZeroRow explains why a conditional attempt UPDATE matched no
// row and, when the caller re-sent the same lease token under a new idempotency
// key, reports an already-completed transition as an idempotent replay instead
// of collapsing every cause into one lease conflict.
//
// A worker that retries after a network timeout with a fresh key must not be
// told its live attempt is invalid: abandoning it would push the attempt into
// UNKNOWN when the lease finally expires, which is exactly what idempotency is
// meant to prevent. Causes are classified in this order:
//  1. attempt missing            → lease conflict (nothing is leaked)
//  2. already the target state   → replay: return the row, applied=false
//  3. token mismatch             → lease conflict (the real state stays hidden)
//  4. lease expired / unknown    → lease conflict
//  5. anything else              → lease conflict
//
// A terminal settle clears lease_token, so an attempt that already reached the
// requested terminal state can never present a matching token; an empty stored
// token on the target state is the consumed lease of that exact outcome.
func classifyAttemptZeroRow(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	leaseToken string,
	idempotencyKey string,
	requestFingerprint string,
	operation domain.WorkflowOperation,
	targetState domain.WorkflowAttemptState,
	now time.Time,
	out *domain.WorkflowAttempt,
) error {
	record, found, err := lookupWorkflowIdempotency(
		ctx, q, idempotencyKey, workflowID, operation, requestFingerprint, "attempt",
	)
	if err != nil {
		return err
	}
	if found {
		// The same key already reserved this transition: answer with its result.
		row, err := q.GetWorkflowAttempt(ctx, domain.WorkflowAttemptID(record.ResultID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrWorkflowAttemptLeaseConflict
		}
		if err != nil {
			return err
		}
		*out = workflowAttemptFromGen(row)
		return nil
	}

	row, err := q.GetWorkflowAttempt(ctx, attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	if err != nil {
		return err
	}
	attempt := workflowAttemptFromGen(row)
	if attempt.WorkflowID != workflowID {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	// A stored token always names the lease that currently owns the attempt; an
	// empty one means the lease was consumed by the transition that already ran.
	if attempt.LeaseToken != "" && attempt.LeaseToken != leaseToken {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	if attempt.State == targetState {
		*out = attempt
		return nil
	}
	if attempt.State == domain.WorkflowAttemptStateUnknown || attempt.LeaseUntil == nil ||
		!attempt.LeaseUntil.After(now) {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	return domain.ErrWorkflowAttemptLeaseConflict
}

// SettleWorkflowAttempt records a terminal attempt outcome and updates the task
// atomically. Failed tasks enter retry_wait only while attempts remain; uncertain
// outcomes are handled separately by RecoverExpiredWorkflowAttempts.
//
// Lease ordering is a TEXT comparison in SQLite, so every instant written here
// is normalized to UTC; mixed offsets would compare chronologically wrong.
func (s *Store) SettleWorkflowAttempt(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	leaseToken string,
	resultState domain.WorkflowAttemptState,
	errorCode string,
	idempotencyKey string,
	requestFingerprint string,
	now time.Time,
) (domain.WorkflowAttempt, bool, error) {
	if resultState != domain.WorkflowAttemptStateSucceeded && resultState != domain.WorkflowAttemptStateFailed {
		return domain.WorkflowAttempt{}, false, domain.ErrInvalidWorkflowTransition
	}
	if strings.TrimSpace(leaseToken) == "" || strings.TrimSpace(idempotencyKey) == "" ||
		len(requestFingerprint) != 64 {
		return domain.WorkflowAttempt{}, false, domain.ErrWorkflowAttemptLeaseConflict
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttempt
	applied := false
	err := s.inSchedulerTx(ctx, "settle workflow attempt", func(q *gen.Queries) error {
		if replay, err := replayExecutionSettlement(ctx, q, workflowID, attemptID, resultState, errorCode, idempotencyKey, requestFingerprint, now, &result); err != nil || replay {
			return err
		}
		if err := executionPermitFence(ctx, q, attemptID, now, true); err != nil {
			return err
		}
		rows, err := q.SettleWorkflowAttempt(ctx, gen.SettleWorkflowAttemptParams{
			NextState: resultState, Now: timePtrToNullTime(&now), ErrorCode: errorCode,
			ID: attemptID, WorkflowID: workflowID, LeaseToken: leaseToken,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return classifyAttemptZeroRow(
				ctx, q, workflowID, attemptID, leaseToken, idempotencyKey, requestFingerprint,
				domain.WorkflowOperationSettleAttempt, resultState, now, &result,
			)
		}

		attemptRow, err := q.GetWorkflowAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		taskRow, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{
			WorkflowID: workflowID, ID: attemptRow.TaskID,
		})
		if err != nil {
			return err
		}
		nextTaskState := domain.WorkflowTaskStateSucceeded
		if resultState == domain.WorkflowAttemptStateFailed {
			nextTaskState = domain.WorkflowTaskStateFailed
			if attemptRow.AttemptNo < taskRow.MaxAttempts {
				nextTaskState = domain.WorkflowTaskStateRetryWait
			}
		}
		if taskRow.State != domain.WorkflowTaskStateClaimed && taskRow.State != domain.WorkflowTaskStateRunning {
			return domain.ErrWorkflowRevisionConflict
		}
		taskRows, err := q.UpdateWorkflowTaskState(ctx, gen.UpdateWorkflowTaskStateParams{
			NextState: nextTaskState, UpdatedAt: now, WorkflowID: workflowID,
			TaskID: attemptRow.TaskID, ExpectedState: taskRow.State, ExpectedRevision: taskRow.Revision,
		})
		if err != nil {
			return err
		}
		if taskRows == 0 {
			return domain.ErrWorkflowRevisionConflict
		}
		if nextTaskState == domain.WorkflowTaskStateFailed {
			if _, err := q.FailWorkflowIfAnyTaskFailed(ctx, gen.FailWorkflowIfAnyTaskFailedParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
		}
		if resultState == domain.WorkflowAttemptStateSucceeded {
			if _, err := q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
			if _, err := q.CompleteWorkflowIfAllTasksSucceeded(ctx, gen.CompleteWorkflowIfAllTasksSucceededParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: workflowID, Operation: domain.WorkflowOperationSettleAttempt,
			Fingerprint: requestFingerprint, ResultType: "attempt", ResultID: string(attemptID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		result, applied = workflowAttemptFromGen(attemptRow), true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttempt{}, false, fmt.Errorf("settle workflow attempt %s: %w", attemptID, err)
	}
	return result, applied, nil
}

// ResolveUnknownWorkflowAttempt records an operator's decided outcome for an
// attempt whose lease expired, so a blocked task can move again. It is the
// only way out of the fail-closed UNKNOWN state, and it never re-dispatches:
// it accepts the outcome a human or a reconciliation job established.
//
// Resolving to succeeded moves the attempt out of the
// idx_workflow_attempts_one_active_per_task partial index, which is what makes
// the task re-claimable or completable again. Resolving to failed leaves the
// task in retry_wait while attempt budget remains and in failed otherwise.
// Either way the task leaves 'blocked', so the task UPDATE is fenced only by
// the caller's expected revision (a state-generic compare-and-swap) and not by
// a hard-coded source state.
func (s *Store) ResolveUnknownWorkflowAttempt(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	resultState domain.WorkflowAttemptState,
	errorCode string,
	expectedTaskRevision int64,
	idempotencyKey string,
	requestFingerprint string,
	now time.Time,
) (domain.WorkflowAttempt, bool, error) {
	if resultState != domain.WorkflowAttemptStateSucceeded && resultState != domain.WorkflowAttemptStateFailed {
		return domain.WorkflowAttempt{}, false, domain.ErrInvalidWorkflowTransition
	}
	if strings.TrimSpace(idempotencyKey) == "" || len(requestFingerprint) != 64 {
		return domain.WorkflowAttempt{}, false, domain.ErrInvalidWorkflowTransition
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttempt
	applied := false
	err := s.inTx(ctx, "resolve unknown workflow attempt", func(q *gen.Queries) error {
		rows, err := q.ResolveUnknownWorkflowAttempt(ctx, gen.ResolveUnknownWorkflowAttemptParams{
			NextState: resultState, Now: timePtrToNullTime(&now), ErrorCode: errorCode,
			ID: attemptID, WorkflowID: workflowID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			record, found, err := lookupWorkflowIdempotency(
				ctx, q, idempotencyKey, workflowID, domain.WorkflowOperationResolveAttempt,
				requestFingerprint, "attempt",
			)
			if err != nil {
				return err
			}
			if found {
				row, err := q.GetWorkflowAttempt(ctx, domain.WorkflowAttemptID(record.ResultID))
				if errors.Is(err, sql.ErrNoRows) {
					return domain.ErrWorkflowAttemptLeaseConflict
				}
				if err != nil {
					return err
				}
				result = workflowAttemptFromGen(row)
				return nil
			}
			// Nothing was resolved and no key reserved it: either the attempt is
			// gone (nothing was leaked, so report the lease) or it was never
			// UNKNOWN, which is an illegal transition rather than a lost lease.
			row, err := q.GetWorkflowAttempt(ctx, attemptID)
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrWorkflowAttemptLeaseConflict
			}
			if err != nil {
				return err
			}
			if existing := workflowAttemptFromGen(row); existing.WorkflowID != workflowID {
				return domain.ErrWorkflowAttemptLeaseConflict
			} else if existing.State != domain.WorkflowAttemptStateUnknown {
				return domain.ErrInvalidWorkflowTransition
			}
			// Still UNKNOWN means the guarded UPDATE could not have matched;
			// report the lease rather than inventing a transition verdict.
			return domain.ErrWorkflowAttemptLeaseConflict
		}

		attemptRow, err := q.GetWorkflowAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		taskRow, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{
			WorkflowID: workflowID, ID: attemptRow.TaskID,
		})
		if err != nil {
			return err
		}
		// The caller's task revision is the reconciliation CAS: two operators
		// deciding different outcomes for one expired attempt cannot both apply.
		if taskRow.Revision != expectedTaskRevision {
			return domain.ErrWorkflowRevisionConflict
		}
		nextTaskState := domain.WorkflowTaskStateSucceeded
		if resultState == domain.WorkflowAttemptStateFailed {
			nextTaskState = domain.WorkflowTaskStateFailed
			if attemptRow.AttemptNo < taskRow.MaxAttempts {
				nextTaskState = domain.WorkflowTaskStateRetryWait
			}
		}
		taskRows, err := q.UpdateWorkflowTaskState(ctx, gen.UpdateWorkflowTaskStateParams{
			NextState: nextTaskState, UpdatedAt: now, WorkflowID: workflowID,
			TaskID: attemptRow.TaskID, ExpectedState: taskRow.State, ExpectedRevision: taskRow.Revision,
		})
		if err != nil {
			return err
		}
		if taskRows == 0 {
			return domain.ErrWorkflowRevisionConflict
		}
		if nextTaskState == domain.WorkflowTaskStateFailed {
			// A workflow already failed by the same terminal outcome is left
			// alone: the query is 'active'-guarded and no-ops there.
			if _, err := q.FailWorkflowIfAnyTaskFailed(ctx, gen.FailWorkflowIfAnyTaskFailedParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
		}
		if resultState == domain.WorkflowAttemptStateSucceeded {
			if _, err := q.RefreshWorkflowReadyTasks(ctx, gen.RefreshWorkflowReadyTasksParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
			if _, err := q.CompleteWorkflowIfAllTasksSucceeded(ctx, gen.CompleteWorkflowIfAllTasksSucceededParams{
				UpdatedAt: now, WorkflowID: workflowID,
			}); err != nil {
				return err
			}
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: workflowID, Operation: domain.WorkflowOperationResolveAttempt,
			Fingerprint: requestFingerprint, ResultType: "attempt", ResultID: string(attemptID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		result, applied = workflowAttemptFromGen(attemptRow), true
		return nil
	})
	if errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		return s.loadIdempotentAttempt(
			ctx, idempotencyKey, workflowID, domain.WorkflowOperationResolveAttempt, requestFingerprint,
		)
	}
	if err != nil {
		return domain.WorkflowAttempt{}, false, fmt.Errorf("resolve unknown workflow attempt %s: %w", attemptID, err)
	}
	return result, applied, nil
}

// RetryWorkflowTask re-readies a retry_wait task only if its dependencies are
// still successful and the configured attempt budget has not been exhausted.
func (s *Store) RetryWorkflowTask(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	expectedRevision int64,
	idempotencyKey string,
	requestFingerprint string,
	now time.Time,
) (domain.WorkflowTask, bool, error) {
	if strings.TrimSpace(idempotencyKey) == "" || len(requestFingerprint) != 64 {
		return domain.WorkflowTask{}, false, domain.ErrInvalidWorkflowTask
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowTask
	applied := false
	err := s.inTx(ctx, "retry workflow task", func(q *gen.Queries) error {
		rows, err := q.RetryWorkflowTask(ctx, gen.RetryWorkflowTaskParams{
			UpdatedAt: now, WorkflowID: workflowID, TaskID: taskID, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			record, found, err := lookupWorkflowIdempotency(
				ctx, q, idempotencyKey, workflowID, domain.WorkflowOperationRetryTask,
				requestFingerprint, "task",
			)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowRevisionConflict
			}
			row, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{
				WorkflowID: workflowID, ID: domain.WorkflowTaskID(record.ResultID),
			})
			if err != nil {
				return err
			}
			result = workflowTaskFromGen(row)
			return nil
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{
			Key: idempotencyKey, WorkflowID: workflowID, Operation: domain.WorkflowOperationRetryTask,
			Fingerprint: requestFingerprint, ResultType: "task", ResultID: string(taskID),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		row, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: workflowID, ID: taskID})
		if err != nil {
			return err
		}
		result, applied = workflowTaskFromGen(row), true
		return nil
	})
	if errors.Is(err, domain.ErrWorkflowIdempotencyConflict) {
		return s.loadIdempotentTask(
			ctx, idempotencyKey, workflowID, domain.WorkflowOperationRetryTask, requestFingerprint,
		)
	}
	if err != nil {
		return domain.WorkflowTask{}, false, fmt.Errorf("retry workflow task %s/%s: %w", workflowID, taskID, err)
	}
	return result, applied, nil
}

// RecoverExpiredWorkflowAttempts fails closed: an expired lease becomes UNKNOWN
// and its task becomes blocked instead of being automatically dispatched again.
//
// Lease expiry is decided by a TEXT comparison in SQLite, so the comparison
// instant is normalized to UTC; a caller's local-zone clock would otherwise
// sort on the wrong side of every stored lease.
func (s *Store) RecoverExpiredWorkflowAttempts(ctx context.Context, now time.Time) (int64, error) {
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var expired int64
	err := s.inTx(ctx, "recover expired workflow attempts", func(q *gen.Queries) error {
		var err error
		expired, err = recoverExpiredWorkflowAttempts(ctx, q, now)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("recover expired workflow attempts: %w", err)
	}
	return expired, nil
}

// recoverExpiredWorkflowAttempts 只把过期执行标为 UNKNOWN，不释放占位或推断已经死亡。
func recoverExpiredWorkflowAttempts(ctx context.Context, q *gen.Queries, now time.Time) (int64, error) {
	rows, err := q.MarkExpiredWorkflowAttemptsUnknown(ctx, timePtrToNullTime(&now))
	if err != nil {
		return 0, err
	}
	if _, err := q.BlockTasksWithUnknownWorkflowAttempts(ctx, now); err != nil {
		return 0, err
	}
	return rows, nil
}

// RecoverExpiredWorkflowsAuthorized 将全局 Operator 恢复和审批审计一起提交，重放仅返回原计数。
func (s *Store) RecoverExpiredWorkflowsAuthorized(ctx context.Context, change domain.PolicyChange) (int64, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var count int64
	var replay bool
	err := s.inSchedulerTx(ctx, "authorized workflow recovery", func(q *gen.Queries) error {
		var err error
		replay, err = policyChangeReplay(ctx, q, "resource", "global:recovery", change, &count)
		if err != nil || replay {
			return err
		}
		count, err = recoverExpiredWorkflowAttempts(ctx, q, change.Now.UTC())
		if err != nil {
			return err
		}
		return insertPolicyChange(ctx, q, "resource", "global:recovery", fmt.Sprint(count), change)
	})
	return count, !replay && err == nil, err
}

func (s *Store) loadIdempotentWorkflow(
	ctx context.Context,
	key string,
	workflowID domain.WorkflowID,
	operation domain.WorkflowOperation,
	fingerprint string,
) (domain.Workflow, bool, error) {
	row, found, err := s.lookupWorkflowIdempotency(ctx, key, workflowID, operation, fingerprint, "workflow")
	if err != nil {
		return domain.Workflow{}, false, err
	}
	if !found {
		return domain.Workflow{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	rec, exists, err := s.GetWorkflow(ctx, domain.WorkflowID(row.ResultID))
	if err != nil {
		return domain.Workflow{}, false, err
	}
	if !exists {
		return domain.Workflow{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	return rec, false, nil
}

func (s *Store) loadIdempotentTask(
	ctx context.Context,
	key string,
	workflowID domain.WorkflowID,
	operation domain.WorkflowOperation,
	fingerprint string,
) (domain.WorkflowTask, bool, error) {
	row, found, err := s.lookupWorkflowIdempotency(ctx, key, workflowID, operation, fingerprint, "task")
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	if !found {
		return domain.WorkflowTask{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	rec, exists, err := s.GetWorkflowTask(ctx, workflowID, domain.WorkflowTaskID(row.ResultID))
	if err != nil {
		return domain.WorkflowTask{}, false, err
	}
	if !exists {
		return domain.WorkflowTask{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	return rec, false, nil
}

func (s *Store) loadIdempotentAttempt(
	ctx context.Context,
	key string,
	workflowID domain.WorkflowID,
	operation domain.WorkflowOperation,
	fingerprint string,
) (domain.WorkflowAttempt, bool, error) {
	row, found, err := s.lookupWorkflowIdempotency(ctx, key, workflowID, operation, fingerprint, "attempt")
	if err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	if !found {
		return domain.WorkflowAttempt{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	rec, exists, err := s.GetWorkflowAttempt(ctx, domain.WorkflowAttemptID(row.ResultID))
	if err != nil {
		return domain.WorkflowAttempt{}, false, err
	}
	if !exists {
		return domain.WorkflowAttempt{}, false, domain.ErrWorkflowIdempotencyConflict
	}
	return rec, false, nil
}
