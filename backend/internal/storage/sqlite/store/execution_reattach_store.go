package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// PrepareSessionReattach 为现存 Host 生成观察许可；不读取资源、预算，也不新建物理占位。
// Workflow 的已过期/UNKNOWN 租约允许观察，业务动作仍必须使用独立的 live 执行许可。
func (s *Store) PrepareSessionReattach(ctx context.Context, expected domain.SessionRecord, fence domain.SchedulerFence, now time.Time) (domain.SessionReattachPermit, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	p := domain.SessionReattachPermit{SessionID: expected.ID, Owner: expected.ControllerOwner(), Fence: fence}
	err := s.inSchedulerTx(ctx, "prepare existing provider reattach", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, fence, now, true); err != nil {
			return err
		}
		row, err := q.GetSession(ctx, expected.ID)
		if err != nil {
			return err
		}
		rec := getSessionRowToRecord(row)
		if rec.IsTerminated || rec.Mode != domain.SessionModeChat || rec.Metadata.ProviderConversationID == "" || rec.Metadata.ControllerGeneration == "" || rec.ControllerOwner() != p.Owner {
			return domain.ErrSchedulerAdmissionConflict
		}
		ad, err := q.GetOccupiedSessionAdmission(ctx, schedulerString(string(rec.ID)))
		if err == nil {
			p.Admission = schedulerAdmissionFromGen(ad)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else {
			count, err := q.CountSessionAdmissionHistory(ctx, schedulerString(string(rec.ID)))
			if err != nil {
				return err
			}
			if count != 0 {
				return domain.ErrSchedulerAdmissionConflict
			}
		}
		binding, err := q.GetWorkflowAttemptBindingBySession(ctx, &rec.ID)
		if err == nil {
			if binding.ControllerGeneration != rec.Metadata.ControllerGeneration {
				return domain.ErrSchedulerAdmissionConflict
			}
			p.Execution, err = loadReattachExecution(ctx, q, binding)
			if err != nil {
				return err
			}
			if p.Execution.SessionID != rec.ID || p.Execution.AdmissionID != p.Admission.ID || p.Admission.ID != "" && p.Execution.Fence != fence {
				return domain.ErrSchedulerFence
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return validateSessionReattach(ctx, q, p, "", now)
	})
	return p, err
}

// loadReattachExecution 核对 Binding、Attempt、Session 的同一归属；M2 无 Admission 时只保留观察快照。
// legacy 快照没有 Admission/Fence，不能通过独立的 loadExecutionPermit 业务门禁。
func loadReattachExecution(ctx context.Context, q *gen.Queries, binding gen.WorkflowAttemptBinding) (domain.ExecutionPermit, error) {
	a, err := q.GetWorkflowAttempt(ctx, binding.AttemptID)
	if err != nil {
		return domain.ExecutionPermit{}, err
	}
	if binding.SessionID == nil || a.SessionID == nil || *binding.SessionID != *a.SessionID || binding.WorkflowID != a.WorkflowID || binding.TaskID != a.TaskID {
		return domain.ExecutionPermit{}, domain.ErrSchedulerAdmissionConflict
	}
	p, err := loadExecutionPermit(ctx, q, binding.AttemptID)
	if errors.Is(err, domain.ErrAdmissionRequired) {
		p = domain.ExecutionPermit{AttemptID: a.ID, WorkflowID: a.WorkflowID, TaskID: a.TaskID, WorkerID: a.WorkerID,
			LeaseToken: a.LeaseToken, AttemptUpdatedAt: a.UpdatedAt, AttemptState: a.State, SessionID: *a.SessionID,
			BindingRevision: binding.Revision, Generation: binding.ControllerGeneration}
		if a.LeaseUntil.Valid {
			p.LeaseUntil = a.LeaseUntil.Time
		}
		return p, nil
	}
	if err != nil {
		return domain.ExecutionPermit{}, err
	}
	if p.SessionID != *binding.SessionID {
		return domain.ExecutionPermit{}, domain.ErrSchedulerAdmissionConflict
	}
	return p, nil
}

// ValidateSessionReattach 在 Provider I/O 前或完成回调时核对同一快照；generation 非空表示核对已提交的新观察代际。
func (s *Store) ValidateSessionReattach(ctx context.Context, p domain.SessionReattachPermit, generation string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "validate existing provider reattach", func(q *gen.Queries) error { return validateSessionReattach(ctx, q, p, generation, now) })
}

// validateSessionReattach 不使用 live 租约门禁；完整快照 CAS 只保护归属，绝不续租或改变 Attempt 状态。
func validateSessionReattach(ctx context.Context, q *gen.Queries, p domain.SessionReattachPermit, generation string, now time.Time) error {
	if err := schedulerFence(ctx, q, p.Fence, now, true); err != nil {
		return err
	}
	row, err := q.GetSession(ctx, p.SessionID)
	if err != nil {
		return err
	}
	rec := getSessionRowToRecord(row)
	wantOwner := p.Owner
	wantAdmission := p.Admission
	wantExecution := p.Execution
	if generation != "" {
		wantOwner.ControllerGeneration = generation
		if wantAdmission.ID != "" {
			wantAdmission.ControllerGeneration = generation
			wantAdmission.State = domain.AdmissionActive
			wantAdmission.Revision++
		}
		if wantExecution.AttemptID != "" {
			wantExecution.Generation = generation
			wantExecution.BindingRevision++
			if wantExecution.AdmissionID != "" {
				wantExecution.AdmissionRevision++
			}
		}
	}
	if rec.IsTerminated || rec.ControllerOwner() != wantOwner {
		return domain.ErrSchedulerAdmissionConflict
	}
	ad, err := q.GetOccupiedSessionAdmission(ctx, schedulerString(string(p.SessionID)))
	if wantAdmission.ID == "" {
		if !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			return domain.ErrSchedulerAdmissionConflict
		}
		count, err := q.CountSessionAdmissionHistory(ctx, schedulerString(string(p.SessionID)))
		if err != nil {
			return err
		}
		if count != 0 {
			return domain.ErrSchedulerAdmissionConflict
		}
	} else {
		if err != nil {
			return err
		}
		if schedulerAdmissionFromGen(ad) != wantAdmission || wantAdmission.Fence != p.Fence || (ad.State != domain.AdmissionActive && ad.State != domain.AdmissionUncertain) || ad.ControllerGeneration != "" && ad.ControllerGeneration != wantOwner.ControllerGeneration {
			return domain.ErrSchedulerAdmissionConflict
		}
	}
	if wantExecution.AttemptID != "" {
		binding, err := q.GetWorkflowAttemptBinding(ctx, wantExecution.AttemptID)
		if err != nil {
			return err
		}
		current, err := loadReattachExecution(ctx, q, binding)
		if err != nil {
			return err
		}
		if current != wantExecution {
			return domain.ErrSchedulerFence
		}
	} else if _, err := q.GetWorkflowAttemptBindingBySession(ctx, &p.SessionID); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return domain.ErrSchedulerAdmissionConflict
	}
	return nil
}

// commitReattachedGeneration 与 Session 的 generation claim 共用一个事务，旧 epoch 的迟到连接不能覆盖新主人。
func commitReattachedGeneration(ctx context.Context, q *gen.Queries, p domain.SessionReattachPermit, generation string) error {
	if p.Admission.ID != "" {
		n, err := q.ReattachSessionAdmissionGeneration(ctx, gen.ReattachSessionAdmissionGenerationParams{ID: p.Admission.ID, Revision: p.Admission.Revision, OwnerID: p.Fence.OwnerID, Epoch: p.Fence.Epoch, Generation: generation})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
	}
	if p.Execution.AttemptID != "" {
		n, err := q.ReattachWorkflowBindingGeneration(ctx, gen.ReattachWorkflowBindingGenerationParams{AttemptID: p.Execution.AttemptID, SessionID: &p.SessionID, PreviousGeneration: p.Owner.ControllerGeneration, Revision: p.Execution.BindingRevision, Generation: generation})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
		if err := bindPathReservations(ctx, q, p.Execution.AttemptID, p.SessionID, generation, p.Fence); err != nil {
			return err
		}
	}
	return nil
}
