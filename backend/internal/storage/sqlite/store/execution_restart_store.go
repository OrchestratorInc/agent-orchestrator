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

// AdmitSessionRestart 在外部 Controller 启动前接受一个新代际。现存活跃槽可复用；
// 已释放或未纳管的普通 Session 必须重新通过容量和预算，历史准入不覆盖。
func (s *Store) AdmitSessionRestart(ctx context.Context, expected domain.SessionRecord, generation string, evidence domain.AdmissionEvidence) error {
	if strings.TrimSpace(generation) == "" || generation == domain.SessionExecutionGeneration(expected) {
		return domain.ErrSchedulerAdmissionConflict
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "admit session restart", func(q *gen.Queries) error {
		row, err := q.GetSession(ctx, expected.ID)
		if err != nil {
			return err
		}
		rec := getSessionRowToRecord(row)
		oldGeneration := domain.SessionExecutionGeneration(rec)
		if !rec.UpdatedAt.Equal(expected.UpdatedAt) || rec.IsTerminated != expected.IsTerminated || rec.Mode != expected.Mode || rec.Harness != expected.Harness || oldGeneration != domain.SessionExecutionGeneration(expected) || rec.IsTaskPreparation {
			return domain.ErrSchedulerAdmissionConflict
		}
		occupied, err := q.GetOccupiedSessionAdmission(ctx, schedulerString(string(rec.ID)))
		if err == nil {
			ad := schedulerAdmissionFromGen(occupied)
			transitionGap, err := admittedTransitionGap(ctx, q, rec, ad)
			if err != nil {
				return err
			}
			// 接受后 Session 尚未发布新代际代表在途或 UNKNOWN，不能再启动。
			if ad.State != domain.AdmissionActive || ad.ControllerGeneration != "" && ad.ControllerGeneration != oldGeneration || oldGeneration == "" && !transitionGap {
				return domain.ErrSchedulerAdmissionConflict
			}
			if ad.AttemptID != "" {
				if err := executionPermitFence(ctx, q, ad.AttemptID, evidence.Now, true); err != nil {
					return err
				}
			} else if ad.Fence != evidence.Fence {
				return domain.ErrSchedulerFence
			}
			if err := schedulerFence(ctx, q, ad.Fence, evidence.Now, true); err != nil {
				return err
			}
			n, err := q.AcceptSessionRestart(ctx, gen.AcceptSessionRestartParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.Fence.OwnerID, Epoch: ad.Fence.Epoch, Generation: generation, Now: evidence.Now.UTC()})
			if err == nil && n != 1 {
				return domain.ErrSchedulerAdmissionConflict
			}
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := q.GetWorkflowAttemptBindingBySession(ctx, &rec.ID); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			return domain.ErrAdmissionRequired
		}
		if err := schedulerFence(ctx, q, evidence.Fence, evidence.Now, true); err != nil {
			return err
		}
		fingerprint := domain.SessionAdmissionFingerprint(string(rec.ID) + ":" + oldGeneration + ":" + rec.UpdatedAt.String())
		ad := domain.SchedulerAdmission{ID: "admission-" + uuid.NewString(), SessionID: rec.ID, ControllerGeneration: generation, ProjectID: rec.ProjectID, Harness: rec.Harness, Mode: domain.NormalizeSessionMode(rec.Mode), ClientRequestID: "session-restart-" + fingerprint, RequestFingerprint: fingerprint, Fence: evidence.Fence, State: domain.AdmissionActive, Revision: 1, CreatedAt: evidence.Now.UTC(), UpdatedAt: evidence.Now.UTC()}
		reservations, err := checkAdmission(ctx, q, ad, evidence)
		if err != nil {
			return err
		}
		return persistAdmission(ctx, q, ad, reservations, evidence)
	})
}

// admittedTransitionGap 仅认可 Lifecycle 已清空执行归属且持久切换仍拥有该 Session 的间隙。
// 接受启动会填入新代际，因此同一间隙不能被重复消费。
func admittedTransitionGap(ctx context.Context, q *gen.Queries, rec domain.SessionRecord, ad domain.SchedulerAdmission) (bool, error) {
	if domain.SessionExecutionGeneration(rec) != "" || ad.ControllerGeneration != "" || ad.AttemptID != "" {
		return false, nil
	}
	transition, err := q.GetActiveSessionInterfaceTransition(ctx, rec.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return transition.Phase == domain.SessionInterfaceTransitionSourceStopping || transition.Phase == domain.SessionInterfaceTransitionSourceStopped || transition.Phase == domain.SessionInterfaceTransitionTargetStarting || transition.Phase == domain.SessionInterfaceTransitionActivating, nil
}

// resetControllerEpochAdmission 与 Lifecycle 的模式 CAS 同事务清空已停止执行的代际。
// 只调整普通 Session 的既有槽，不创建预留、不释放预算、也不放行 uncertain。
func resetControllerEpochAdmission(ctx context.Context, q *gen.Queries, id domain.SessionID, now time.Time) error {
	ad, err := q.GetOccupiedSessionAdmission(ctx, schedulerString(string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ad.AttemptID.Valid || ad.State != domain.AdmissionActive {
		return domain.ErrSchedulerAdmissionConflict
	}
	_, err = q.AcceptSessionRestart(ctx, gen.AcceptSessionRestartParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.OwnerID, Epoch: ad.Epoch, Now: now.UTC()})
	return err
}

// releaseDeletedSessionAdmission 只在同事务持久终止且代际一致时释放槽；未知费用继续保留。
func releaseDeletedSessionAdmission(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	row, err := q.GetSession(ctx, id)
	if err != nil {
		return err
	}
	rec := getSessionRowToRecord(row)
	if !rec.IsTerminated {
		return domain.ErrSchedulerAdmissionConflict
	}
	ad, err := q.GetOccupiedSessionAdmission(ctx, schedulerString(string(id)))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && ad.ControllerGeneration != "" && ad.ControllerGeneration != domain.SessionExecutionGeneration(rec) {
		return domain.ErrSchedulerAdmissionConflict
	}
	if err := q.FreezeDeletedSessionBudgets(ctx, schedulerString(string(id))); err != nil {
		return err
	}
	return q.ReleaseDeletedSessionAdmissions(ctx, schedulerString(string(id)))
}

// syncReanchoredAdmissionGeneration 只跟随 Session 已发布的同一代际；观测先到时保持旧值，
// 防止尚未持久化的 Controller 被当作预算结算依据。
func syncReanchoredAdmissionGeneration(ctx context.Context, q *gen.Queries, attemptID domain.WorkflowAttemptID, sessionID domain.SessionID, generation string, now time.Time) error {
	row, err := q.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if domain.SessionExecutionGeneration(getSessionRowToRecord(row)) != generation {
		return nil
	}
	ad, err := q.GetSchedulerAdmissionByAttempt(ctx, schedulerString(string(attemptID)))
	if err != nil {
		return err
	}
	if ad.State != domain.AdmissionActive || ad.SessionID.String != string(sessionID) || ad.ControllerGeneration == generation {
		return nil
	}
	_, err = q.AcceptSessionRestart(ctx, gen.AcceptSessionRestartParams{ID: ad.ID, Revision: ad.Revision, OwnerID: ad.OwnerID, Epoch: ad.Epoch, Generation: generation, Now: now.UTC()})
	return err
}
