package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ReserveAndClaim 在同一 writer 事务内验证任期、资源、所有预算及 DAG，并写入唯一 Attempt。
func (s *Store) ReserveAndClaim(ctx context.Context, in domain.ClaimAdmissionInput) (domain.WorkflowAttempt, domain.SchedulerAdmission, bool, error) {
	if err := validateAdmissionAttempt(in.Attempt, in.IdempotencyKey, in.RequestFingerprint); err != nil {
		return domain.WorkflowAttempt{}, domain.SchedulerAdmission{}, false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var attempt domain.WorkflowAttempt
	var admission domain.SchedulerAdmission
	var created bool
	err := s.inSchedulerTx(ctx, "reserve and claim", func(q *gen.Queries) error {
		var found bool
		var err error
		attempt, admission, found, err = findWorkflowClaimAdmission(ctx, q, in.Attempt.WorkflowID, in.IdempotencyKey, in.RequestFingerprint)
		if err != nil || found {
			return err
		}
		if err := schedulerFence(ctx, q, in.Evidence.Fence, in.Evidence.Now, true); err != nil {
			return err
		}
		if !in.Attempt.LeaseUntil.After(in.Evidence.Now) {
			return domain.ErrInvalidWorkflowAttemptLease
		}
		w, err := q.GetWorkflow(ctx, in.Attempt.WorkflowID)
		if err != nil {
			return err
		}
		if w.Revision != in.ExpectedWorkflowRevision {
			return domain.ErrWorkflowRevisionConflict
		}
		t, err := q.GetWorkflowTask(ctx, gen.GetWorkflowTaskParams{WorkflowID: in.Attempt.WorkflowID, ID: in.Attempt.TaskID})
		if err != nil {
			return err
		}
		if t.State != domain.WorkflowTaskStateReady || t.Revision != in.ExpectedTaskRevision || w.State != domain.WorkflowStateActive {
			return domain.ErrWorkflowTaskNotClaimable
		}
		if t.Profile != in.Evidence.Capability.Profile || t.AgentHarness != "" && t.AgentHarness != in.Evidence.Capability.Harness {
			return domain.ErrCapabilityEvidenceInsufficient
		}
		if err := checkPredecessorQuality(ctx, q, in.Attempt.WorkflowID, in.Attempt.TaskID, t.Kind, in.Evidence.Now); err != nil {
			return err
		}
		states, err := q.ListSchedulerWorkflowStates(ctx)
		if err != nil {
			return err
		}
		for _, state := range states {
			if state.WorkflowID == string(in.Attempt.WorkflowID) && state.Paused == 1 {
				return domain.ErrSchedulerCapacity
			}
		}
		due, err := q.ListSchedulerRetryDue(ctx)
		if err != nil {
			return err
		}
		for _, r := range due {
			if r.WorkflowID == string(in.Attempt.WorkflowID) && r.TaskID == string(in.Attempt.TaskID) && r.NextEligibleAt.After(in.Evidence.Now) {
				return domain.ErrWorkflowTaskNotClaimable
			}
		}
		admission = domain.SchedulerAdmission{ID: "admission-" + uuid.NewString(), AttemptID: in.Attempt.ID, WorkflowID: in.Attempt.WorkflowID, TaskID: in.Attempt.TaskID, Harness: in.Evidence.Capability.Harness, RequestFingerprint: in.RequestFingerprint, Fence: in.Evidence.Fence, State: domain.AdmissionReserved, Revision: 1, CreatedAt: in.Evidence.Now.UTC(), UpdatedAt: in.Evidence.Now.UTC()}
		reservations, err := checkAdmission(ctx, q, admission, in.Evidence)
		if err != nil {
			return err
		}
		attempt = in.Attempt
		attempt.CreatedAt = utcTime(attempt.CreatedAt)
		attempt.UpdatedAt = utcTime(attempt.UpdatedAt)
		attempt.LeaseUntil = utcTimePtr(attempt.LeaseUntil)
		n, err := q.ClaimWorkflowTask(ctx, gen.ClaimWorkflowTaskParams{UpdatedAt: attempt.UpdatedAt, WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID, ExpectedRevision: in.ExpectedTaskRevision})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrWorkflowTaskNotClaimable
		}
		attempt.AttemptNo, err = q.GetNextWorkflowAttemptNo(ctx, gen.GetNextWorkflowAttemptNoParams{WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID})
		if err != nil {
			return err
		}
		if err := q.InsertWorkflowAttempt(ctx, workflowAttemptToInsert(attempt)); err != nil {
			return err
		}
		if err := persistAdmission(ctx, q, admission, reservations, in.Evidence); err != nil {
			return err
		}
		if err := reserveTaskPaths(ctx, q, attempt, admission); err != nil {
			return err
		}
		if err := insertWorkflowIdempotency(ctx, q, workflowIdempotencyInsert{Key: in.IdempotencyKey, WorkflowID: attempt.WorkflowID, Operation: domain.WorkflowOperationClaimTask, Fingerprint: in.RequestFingerprint, ResultType: "attempt", ResultID: string(attempt.ID), CreatedAt: attempt.CreatedAt}); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttempt{}, domain.SchedulerAdmission{}, false, err
	}
	return attempt, admission, created, nil
}

// validateAdmissionAttempt 保留旧 Claim 的错误分类；无证据入口也不能改变参数校验结果。
func validateAdmissionAttempt(a domain.WorkflowAttempt, key, fp string) error {
	if strings.TrimSpace(key) == "" || len(fp) != 64 || a.State != domain.WorkflowAttemptStateClaimed || a.AttemptNo != 0 {
		return domain.ErrInvalidWorkflowAttempt
	}
	v := a
	v.AttemptNo = 1
	if err := v.Validate(); err != nil {
		return err
	}
	if a.LeaseUntil == nil || !a.LeaseUntil.After(a.UpdatedAt) {
		return domain.ErrInvalidWorkflowAttemptLease
	}
	return nil
}

// ReserveSessionAdmission 为手动启动提供相同事务门禁；稳定请求键重放不再次占槽。
func (s *Store) ReserveSessionAdmission(ctx context.Context, in domain.SessionAdmissionInput) (domain.SchedulerAdmission, bool, error) {
	if strings.TrimSpace(in.ClientRequestID) == "" || len(in.ClientRequestHash) != 64 || (in.Mode != domain.SessionModeChat && in.Mode != domain.SessionModeTUI) {
		return domain.SchedulerAdmission{}, false, domain.ErrSchedulerAdmissionConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.SchedulerAdmission
	var created bool
	err := s.inSchedulerTx(ctx, "reserve session admission", func(q *gen.Queries) error {
		r, err := q.GetSchedulerAdmissionByRequest(ctx, schedulerString(in.ClientRequestID))
		if err == nil {
			result = schedulerAdmissionFromGen(r)
			if result.RequestFingerprint != in.ClientRequestHash || result.ProjectID != in.ProjectID || result.Harness != in.Harness || result.Mode != in.Mode {
				return domain.ErrSchedulerAdmissionConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := schedulerFence(ctx, q, in.Evidence.Fence, in.Evidence.Now, true); err != nil {
			return err
		}
		if in.ProjectID != "" {
			if _, err := q.GetProject(ctx, in.ProjectID); err != nil {
				return err
			}
		}
		if in.Harness != in.Evidence.Capability.Harness {
			return domain.ErrCapabilityEvidenceInsufficient
		}
		result = domain.SchedulerAdmission{ID: "admission-" + uuid.NewString(), ProjectID: in.ProjectID, Harness: in.Harness, Mode: in.Mode, ClientRequestID: in.ClientRequestID, RequestFingerprint: in.ClientRequestHash, Fence: in.Evidence.Fence, State: domain.AdmissionReserved, Revision: 1, CreatedAt: in.Evidence.Now.UTC(), UpdatedAt: in.Evidence.Now.UTC()}
		reservations, err := checkAdmission(ctx, q, result, in.Evidence)
		if err != nil {
			return err
		}
		if err := persistAdmission(ctx, q, result, reservations, in.Evidence); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return domain.SchedulerAdmission{}, false, err
	}
	return result, created, nil
}

// FindWorkflowClaimAdmission 返回历史结果，不重新验证或授予执行资格。
func (s *Store) FindWorkflowClaimAdmission(ctx context.Context, w domain.WorkflowID, key, fp string) (domain.WorkflowAttempt, domain.SchedulerAdmission, bool, error) {
	return findWorkflowClaimAdmission(ctx, s.qr, w, key, fp)
}

// findWorkflowClaimAdmission 只认相同指纹和已存在的准入记录，不为历史 Claim 补造授权。
func findWorkflowClaimAdmission(ctx context.Context, q *gen.Queries, w domain.WorkflowID, key, fp string) (domain.WorkflowAttempt, domain.SchedulerAdmission, bool, error) {
	r, found, err := lookupWorkflowIdempotency(ctx, q, key, w, domain.WorkflowOperationClaimTask, fp, "attempt")
	if err != nil || !found {
		return domain.WorkflowAttempt{}, domain.SchedulerAdmission{}, false, err
	}
	a, err := q.GetWorkflowAttempt(ctx, domain.WorkflowAttemptID(r.ResultID))
	if err != nil {
		return domain.WorkflowAttempt{}, domain.SchedulerAdmission{}, false, err
	}
	ad, err := q.GetSchedulerAdmissionByAttempt(ctx, schedulerString(r.ResultID))
	if err != nil {
		return domain.WorkflowAttempt{}, domain.SchedulerAdmission{}, false, domain.ErrAdmissionRequired
	}
	return workflowAttemptFromGen(a), schedulerAdmissionFromGen(ad), true, nil
}

// GetSchedulerAdmission 按稳定标识读取持久占位。
func (s *Store) GetSchedulerAdmission(ctx context.Context, id string) (domain.SchedulerAdmission, bool, error) {
	r, err := s.qr.GetSchedulerAdmission(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SchedulerAdmission{}, false, nil
	}
	return schedulerAdmissionFromGen(r), err == nil, err
}

// GetSchedulerAdmissionForAttempt 读取 Claim 时固定的 Harness，后续配置变化不能改变已获准执行的身份。
func (s *Store) GetSchedulerAdmissionForAttempt(ctx context.Context, id domain.WorkflowAttemptID) (domain.SchedulerAdmission, bool, error) {
	r, err := s.qr.GetSchedulerAdmissionByAttempt(ctx, schedulerString(string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SchedulerAdmission{}, false, nil
	}
	if err != nil {
		return domain.SchedulerAdmission{}, false, err
	}
	return schedulerAdmissionFromGen(r), true, nil
}

// ListSchedulerAdmissions 在恢复时保留所有未释放占位，不依据租约到期过滤。
func (s *Store) ListSchedulerAdmissions(ctx context.Context, occupied bool) ([]domain.SchedulerAdmission, error) {
	flag := 0
	if occupied {
		flag = 1
	}
	rows, err := s.qr.ListSchedulerAdmissions(ctx, flag)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SchedulerAdmission, 0, len(rows))
	for _, r := range rows {
		out = append(out, schedulerAdmissionFromGen(r))
	}
	return out, nil
}

// AdoptSchedulerAdmission 仅接管为 uncertain；恢复本身绝不自动释放或再次执行。
func (s *Store) AdoptSchedulerAdmission(ctx context.Context, id string, revision int64, f domain.SchedulerFence, now time.Time) (domain.SchedulerAdmission, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.SchedulerAdmission
	err := s.inSchedulerTx(ctx, "adopt scheduler admission", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, false); err != nil {
			return err
		}
		n, err := q.AdoptSchedulerAdmission(ctx, gen.AdoptSchedulerAdmissionParams{ID: id, Revision: revision, OwnerID: f.OwnerID, Epoch: f.Epoch, Now: now.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
		r, err := q.GetSchedulerAdmission(ctx, id)
		if err != nil {
			return err
		}
		if err := q.AdoptPathReservations(ctx, gen.AdoptPathReservationsParams{AdmissionID: id, OwnerID: f.OwnerID, Epoch: f.Epoch}); err != nil {
			return err
		}
		result = schedulerAdmissionFromGen(r)
		return err
	})
	return result, err == nil, err
}

func schedulerAdmissionFromGen(r gen.SchedulerAdmission) domain.SchedulerAdmission {
	return domain.SchedulerAdmission{ID: r.ID, AttemptID: domain.WorkflowAttemptID(r.AttemptID.String), WorkflowID: domain.WorkflowID(r.WorkflowID), TaskID: domain.WorkflowTaskID(r.TaskID), SessionID: domain.SessionID(r.SessionID.String), ControllerGeneration: r.ControllerGeneration, ProjectID: domain.ProjectID(r.ProjectID), Harness: domain.AgentHarness(r.Harness), Mode: domain.SessionMode(r.Mode), ClientRequestID: r.ClientRequestID.String, RequestFingerprint: r.RequestFingerprint, Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, State: r.State, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// persistAdmission 把已验证预留与成功决策一起提交，失败时不存在部分预留。
func persistAdmission(ctx context.Context, q *gen.Queries, a domain.SchedulerAdmission, rs []gen.InsertBudgetReservationParams, e domain.AdmissionEvidence) error {
	if err := q.InsertSchedulerAdmission(ctx, gen.InsertSchedulerAdmissionParams{ID: a.ID, AttemptID: schedulerString(string(a.AttemptID)), WorkflowID: string(a.WorkflowID), TaskID: string(a.TaskID), SessionID: schedulerString(string(a.SessionID)), ControllerGeneration: a.ControllerGeneration, ProjectID: string(a.ProjectID), Harness: string(a.Harness), Mode: string(a.Mode), ClientRequestID: schedulerString(a.ClientRequestID), RequestFingerprint: a.RequestFingerprint, OwnerID: a.Fence.OwnerID, Epoch: a.Fence.Epoch, State: a.State, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}); err != nil {
		return err
	}
	for _, r := range rs {
		if err := q.InsertBudgetReservation(ctx, r); err != nil {
			return err
		}
	}
	return insertSchedulerDecision(ctx, q, domain.SchedulerDecision{AdmissionID: a.ID, WorkflowID: a.WorkflowID, TaskID: a.TaskID, Fence: a.Fence, ReasonCode: "ADMITTED", PolicyRevisions: e.ExpectedPolicyRevisions, CreatedAt: e.Now})
}
