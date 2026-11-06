package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateAdmittedSession 在同一事务消费当前任期 Admission，并创建或发布唯一 Session。
// 已接受的启动允许继续在途工作，但重放只能返回原行，不能再次获取启动权。
func (s *Store) CreateAdmittedSession(ctx context.Context, seed domain.SessionRecord, ad domain.SchedulerAdmission, preparationID domain.SessionID, now time.Time) (domain.SessionRecord, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.SessionRecord
	var fresh bool
	err := s.inSchedulerTx(ctx, "accept admitted session", func(q *gen.Queries) error {
		stored, err := q.GetSchedulerAdmission(ctx, ad.ID)
		if err != nil {
			return err
		}
		current := schedulerAdmissionFromGen(stored)
		if current.SessionID != "" {
			r, err := q.GetSession(ctx, current.SessionID)
			if err != nil {
				return err
			}
			result = getSessionRowToRecord(r)
			if result.ClientRequestID != seed.ClientRequestID || result.ClientRequestHash != seed.ClientRequestHash {
				return domain.ErrSchedulerAdmissionConflict
			}
			return nil
		}
		if current.Revision != ad.Revision || current.Fence != ad.Fence || current.State != domain.AdmissionReserved {
			return domain.ErrSchedulerAdmissionConflict
		}
		ad = current
		if err := schedulerFence(ctx, q, ad.Fence, now, true); err != nil {
			return err
		}
		if seed.Harness != ad.Harness || seed.ClientRequestID == "" || seed.ClientRequestHash == "" {
			return domain.ErrSchedulerAdmissionConflict
		}
		if ad.AttemptID != "" {
			if err := executionPermitFence(ctx, q, ad.AttemptID, now, true); err != nil {
				return err
			}
			b, err := q.GetWorkflowAttemptBinding(ctx, ad.AttemptID)
			if err != nil {
				return err
			}
			if b.State != domain.WorkflowBindingStateSpawnRequested || b.SpawnRequestID != seed.ClientRequestID || b.SpawnHash != seed.ClientRequestHash {
				return domain.ErrSchedulerAdmissionConflict
			}
		} else if ad.ClientRequestID != seed.ClientRequestID || ad.RequestFingerprint != domain.SessionAdmissionFingerprint(seed.ClientRequestHash) || ad.ProjectID != seed.ProjectID || ad.Mode != seed.Mode {
			return domain.ErrSchedulerAdmissionConflict
		}
		if preparationID != "" {
			old, err := q.GetSession(ctx, preparationID)
			if err != nil {
				return err
			}
			if old.ProjectID == nil || *old.ProjectID != seed.ProjectID || !old.IsTaskPreparation {
				return domain.ErrSchedulerAdmissionConflict
			}
			ok, err := promoteTaskPreparation(ctx, q, preparationID, seed)
			if err != nil {
				return err
			}
			if !ok {
				return domain.ErrSchedulerAdmissionConflict
			}
			r, err := q.GetSession(ctx, preparationID)
			if err != nil {
				return err
			}
			result = getSessionRowToRecord(r)
			fresh = true
		} else {
			result, fresh, err = s.createSessionWithQueriesLocked(ctx, q, seed)
			if err != nil {
				return err
			}
			if !fresh {
				return domain.ErrSchedulerAdmissionConflict
			}
		}
		n, err := q.AcceptSessionAdmission(ctx, gen.AcceptSessionAdmissionParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, SessionID: schedulerString(string(result.ID)), Now: now.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
		if ad.AttemptID != "" {
			if err := bindPathReservations(ctx, q, ad.AttemptID, result.ID, domain.SessionExecutionGeneration(result), ad.Fence); err != nil {
				return err
			}
		}
		if generation := domain.SessionExecutionGeneration(result); generation != "" {
			_, err = q.AcceptSessionRestart(ctx, gen.AcceptSessionRestartParams{ID: ad.ID, Revision: ad.Revision + 1, OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, Generation: generation, Now: now.UTC()})
			return err
		}
		return nil
	})
	return result, fresh, err
}

// RecoverExecutionAdmissionSession 只关联可从原请求或现存 Binding 证明的 Session，不创建执行。
func (s *Store) RecoverExecutionAdmissionSession(ctx context.Context, ad domain.SchedulerAdmission, observation ports.SessionObservation, now time.Time) error {
	if observation.SessionID == "" || observation.ObservedAt.After(now) || now.Sub(observation.ObservedAt) > 15*time.Second {
		return domain.ErrSchedulerAdmissionConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "recover execution admission session", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, ad.Fence, now, false); err != nil {
			return err
		}
		r, err := q.GetSession(ctx, observation.SessionID)
		if err != nil {
			return err
		}
		rec := getSessionRowToRecord(r)
		key := ad.ClientRequestID
		if ad.AttemptID != "" {
			b, err := q.GetWorkflowAttemptBinding(ctx, ad.AttemptID)
			if err != nil {
				return err
			}
			key = b.SpawnRequestID
		}
		acceptedRestart := ad.AttemptID == "" && ad.SessionID == rec.ID && ad.ControllerGeneration != ""
		if !acceptedRestart && (key == "" || key != rec.ClientRequestID || key != observation.ClientRequestID) {
			return domain.ErrSchedulerAdmissionConflict
		}
		state := domain.AdmissionUncertain
		if !observation.Terminated && observation.Readiness == ports.SessionReadinessReady && observation.ControllerGeneration != "" && observation.ControllerGeneration == domain.SessionExecutionGeneration(rec) && (ad.ControllerGeneration == "" || ad.ControllerGeneration == observation.ControllerGeneration) {
			state = domain.AdmissionActive
		}
		n, err := q.RecoverExecutionAdmissionSession(ctx, gen.RecoverExecutionAdmissionSessionParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, SessionID: schedulerString(string(observation.SessionID)), State: state, Now: now.UTC()})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerAdmissionConflict
		}
		if ad.AttemptID != "" {
			return bindPathReservations(ctx, q, ad.AttemptID, rec.ID, domain.SessionExecutionGeneration(rec), ad.Fence)
		}
		return nil
	})
}
