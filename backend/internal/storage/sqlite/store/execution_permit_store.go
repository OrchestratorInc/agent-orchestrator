package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// LoadExecutionPermit 返回当前任期的内部快照；读取不授予重新启动 UNKNOWN 的权限。
func (s *Store) LoadExecutionPermit(ctx context.Context, id domain.WorkflowAttemptID, f domain.SchedulerFence, now time.Time) (domain.ExecutionPermit, error) {
	p, err := loadExecutionPermit(ctx, s.qr, id)
	if err != nil {
		return p, err
	}
	if p.Fence != f {
		return domain.ExecutionPermit{}, domain.ErrSchedulerFence
	}
	if err := schedulerFence(ctx, s.qr, f, now, false); err != nil {
		return domain.ExecutionPermit{}, err
	}
	return p, nil
}

// ValidateExecutionPermit 在外部执行动作接受前核对原快照，不给旧任期、过期或 UNKNOWN 换发许可。
func (s *Store) ValidateExecutionPermit(ctx context.Context, p domain.ExecutionPermit, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx = ports.WithExecutionPermit(ctx, p)
	return s.inSchedulerTx(ctx, "validate execution action", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, p.AttemptID, now, true); err != nil {
			return err
		}
		if p.SessionID != "" {
			row, err := q.GetSession(ctx, p.SessionID)
			if err != nil {
				return err
			}
			if row.IsTerminated {
				return domain.ErrSchedulerAdmissionConflict
			}
		}
		return nil
	})
}

// loadExecutionPermit 合并唯一 Admission、Attempt 和 Binding，不推断缺失身份。
func loadExecutionPermit(ctx context.Context, q *gen.Queries, id domain.WorkflowAttemptID) (domain.ExecutionPermit, error) {
	ad, err := q.GetSchedulerAdmissionByAttempt(ctx, schedulerString(string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExecutionPermit{}, domain.ErrAdmissionRequired
	}
	if err != nil {
		return domain.ExecutionPermit{}, err
	}
	a, err := q.GetWorkflowAttempt(ctx, id)
	if err != nil {
		return domain.ExecutionPermit{}, err
	}
	p := domain.ExecutionPermit{AdmissionID: ad.ID, AdmissionRevision: ad.Revision, Fence: domain.SchedulerFence{OwnerID: ad.OwnerID, Epoch: ad.Epoch}, AttemptID: id, WorkflowID: a.WorkflowID, TaskID: a.TaskID, WorkerID: a.WorkerID, LeaseToken: a.LeaseToken, AttemptUpdatedAt: a.UpdatedAt, AttemptState: a.State, SessionID: domain.SessionID(ad.SessionID.String)}
	if a.LeaseUntil.Valid {
		p.LeaseUntil = a.LeaseUntil.Time
	}
	b, err := q.GetWorkflowAttemptBinding(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.ExecutionPermit{}, err
	}
	if err == nil {
		p.BindingRevision = b.Revision
		p.Generation = b.ControllerGeneration
	}
	return p, nil
}

// executionPermitFence 在同一 writer 事务中拒绝旧 worker、租约、代际、revision 或任期。
// live=false 只供观测恢复写；它不能放行 Spawn、续租或业务结算。
func executionPermitFence(ctx context.Context, q *gen.Queries, id domain.WorkflowAttemptID, now time.Time, live bool) error {
	want, ok := ports.ExecutionPermitFromContext(ctx)
	if !ok || want.AttemptID != id {
		return domain.ErrAdmissionRequired
	}
	got, err := loadExecutionPermit(ctx, q, id)
	if err != nil {
		return err
	}
	if got != want {
		return domain.ErrSchedulerFence
	}
	if err := schedulerFence(ctx, q, want.Fence, now, live); err != nil {
		return err
	}
	ad, err := q.GetSchedulerAdmission(ctx, want.AdmissionID)
	if err != nil {
		return err
	}
	if ad.State == domain.AdmissionReleased || live && ad.State == domain.AdmissionUncertain {
		return domain.ErrSchedulerAdmissionConflict
	}
	if live && ad.ControllerGeneration != "" && want.Generation != "" && ad.ControllerGeneration != want.Generation {
		return domain.ErrSchedulerAdmissionConflict
	}
	if live && (want.LeaseToken == "" || !want.LeaseUntil.After(now) || (want.AttemptState != domain.WorkflowAttemptStateClaimed && want.AttemptState != domain.WorkflowAttemptStateRunning)) {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	return nil
}

// RenewExecutionLease 只接受当前绑定的可信活跃观测；UNKNOWN、过期和旧 generation 不续租。
func (s *Store) RenewExecutionLease(ctx context.Context, p domain.ExecutionPermit, observation ports.SessionObservation, until, now time.Time) error {
	if observation.Terminated || observation.Readiness != ports.SessionReadinessReady || observation.SessionID != p.SessionID || p.Generation == "" || observation.ControllerGeneration != p.Generation || observation.ObservedAt.After(now) || now.Sub(observation.ObservedAt) > 15*time.Second || !until.After(now) || until.Sub(now) > 5*time.Minute {
		return domain.ErrWorkflowAttemptLeaseConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx = ports.WithExecutionPermit(ctx, p)
	return s.inSchedulerTx(ctx, "renew execution lease", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, p.AttemptID, now, true); err != nil {
			return err
		}
		b, err := q.GetWorkflowAttemptBinding(ctx, p.AttemptID)
		if err != nil {
			return err
		}
		if b.State != domain.WorkflowBindingStateBoundReady && b.State != domain.WorkflowBindingStateRunning {
			return domain.ErrWorkflowAttemptLeaseConflict
		}
		paths, err := q.ListAttemptPathReservations(ctx, string(p.AttemptID))
		if err != nil {
			return err
		}
		for _, path := range paths {
			if path.ReleasedAt.Valid || path.OwnerID != p.Fence.OwnerID || path.Epoch != p.Fence.Epoch || path.SessionID != string(p.SessionID) || path.Generation != p.Generation {
				return domain.ErrSchedulerFence
			}
		}
		n, err := q.RenewExecutionAttemptLease(ctx, gen.RenewExecutionAttemptLeaseParams{ID: p.AttemptID, WorkerID: p.WorkerID, LeaseToken: p.LeaseToken, Now: sql.NullTime{Time: now.UTC(), Valid: true}, LeaseUntil: sql.NullTime{Time: until.UTC(), Valid: true}, ExpectedLeaseUntil: sql.NullTime{Time: p.LeaseUntil, Valid: true}, ExpectedUpdatedAt: p.AttemptUpdatedAt})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrWorkflowAttemptLeaseConflict
		}
		return q.RenewPathReservations(ctx, gen.RenewPathReservationsParams{AttemptID: string(p.AttemptID), OwnerID: p.Fence.OwnerID, Epoch: p.Fence.Epoch,
			SessionID: string(p.SessionID), Generation: p.Generation, LeaseUntil: until.UTC()})
	})
}

// ReleaseExecutionAdmission 仅释放已可信终止且代际匹配的物理槽；预算保持 uncertain 等待对账。
func (s *Store) ReleaseExecutionAdmission(ctx context.Context, ad domain.SchedulerAdmission, observation ports.SessionObservation, now time.Time) error {
	if !observation.Terminated || observation.SessionID == "" || observation.SessionID != ad.SessionID || observation.ObservedAt.After(now) || now.Sub(observation.ObservedAt) > 15*time.Second {
		return domain.ErrSchedulerAdmissionConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "release execution admission", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, ad.Fence, now, false); err != nil {
			return err
		}
		session, err := q.GetSession(ctx, ad.SessionID)
		if err != nil {
			return err
		}
		rec := rowToRecord(session)
		generation := domain.SessionExecutionGeneration(rec)
		if !rec.IsTerminated || ad.ControllerGeneration != "" && ad.ControllerGeneration != generation || observation.ControllerGeneration != "" && generation != observation.ControllerGeneration {
			return domain.ErrSchedulerAdmissionConflict
		}
		if observation.ControllerGeneration == "" && (ad.AttemptID != "" || observation.SessionUpdatedAt.IsZero() || !observation.SessionUpdatedAt.Equal(rec.UpdatedAt)) {
			return domain.ErrSchedulerAdmissionConflict
		}
		if ad.AttemptID != "" {
			b, err := q.GetWorkflowAttemptBinding(ctx, ad.AttemptID)
			if err != nil {
				return err
			}
			if b.ControllerGeneration != observation.ControllerGeneration || b.SessionID == nil || *b.SessionID != ad.SessionID {
				return domain.ErrSchedulerAdmissionConflict
			}
		}
		n, err := q.ReleaseExecutionAdmission(ctx, gen.ReleaseExecutionAdmissionParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, SessionID: schedulerString(string(ad.SessionID)), Now: now.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
		if err := q.FreezeExecutionBudgetReservations(ctx, ad.ID); err != nil {
			return err
		}
		leader, err := q.GetSchedulerLeader(ctx)
		if err != nil {
			return err
		}
		if ad.AttemptID != "" && leader.Recovered == 1 {
			_, err = releaseFrozenPaths(ctx, q, ad.AttemptID, ad.Fence, now)
		}
		return err
	})
}
