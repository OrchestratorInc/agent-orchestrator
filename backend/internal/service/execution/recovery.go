package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// recoveryStore 持有占位、代际和续租事实；恢复只观测已接受执行，不能调用 Spawn。
type recoveryStore interface {
	permitStore
	ListSchedulerAdmissions(context.Context, bool) ([]domain.SchedulerAdmission, error)
	GetSchedulerAdmission(context.Context, string) (domain.SchedulerAdmission, bool, error)
	AdoptSchedulerAdmission(context.Context, string, int64, domain.SchedulerFence, time.Time) (domain.SchedulerAdmission, bool, error)
	RecoverExecutionAdmissionSession(context.Context, domain.SchedulerAdmission, ports.SessionObservation, time.Time) error
	RenewExecutionLease(context.Context, domain.ExecutionPermit, ports.SessionObservation, time.Time, time.Time) error
	ReleaseExecutionAdmission(context.Context, domain.SchedulerAdmission, ports.SessionObservation, time.Time) error
	ReleaseAbandonedExecutionAdmission(context.Context, domain.SchedulerAdmission, time.Time) error
}

// RecoverAdmissions 在新 Leader 开放准入前恢复旧占位和 Session 事实；缺失或未知证据保留占位。
// 单条观测失败不代表 Session 死亡；持久化或任期失败则阻止 CompleteRecovery。
func (c *Coordinator) RecoverAdmissions(ctx context.Context, fence domain.SchedulerFence) error {
	store, ok := c.store.(recoveryStore)
	if !ok {
		return domain.ErrAdmissionRequired
	}
	admissions, err := store.ListSchedulerAdmissions(ctx, true)
	if err != nil {
		return err
	}
	for _, ad := range admissions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ad.Fence != fence {
			var applied bool
			ad, applied, err = store.AdoptSchedulerAdmission(ctx, ad.ID, ad.Revision, fence, c.now().UTC())
			if err != nil {
				return err
			}
			if !applied {
				return domain.ErrSchedulerAdmissionConflict
			}
		}
		observation, found, observeErr := c.admissionObservation(ctx, ad)
		if observeErr != nil || !found {
			if ad.AttemptID == "" && ad.SessionID == "" && ad.State == domain.AdmissionUncertain {
				if err := store.ReleaseAbandonedExecutionAdmission(ctx, ad, c.now().UTC()); err != nil && !expectedObservationConflict(err) {
					return err
				}
			}
			continue
		}
		if err := store.RecoverExecutionAdmissionSession(ctx, ad, observation, c.now().UTC()); err != nil {
			return err
		}
		ad, found, err = store.GetSchedulerAdmission(ctx, ad.ID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrAdmissionRequired
		}
		if ad.AttemptID != "" && !observation.Terminated {
			p, err := store.LoadExecutionPermit(ctx, ad.AttemptID, fence, c.now().UTC())
			if err != nil {
				return err
			}
			// 已结算但未确认物理终止的 Attempt 保留占位，不能借恢复重新协调执行。
			if p.AttemptState != domain.WorkflowAttemptStateClaimed && p.AttemptState != domain.WorkflowAttemptStateRunning {
				continue
			}
			if _, err := c.Reconcile(ports.WithExecutionPermit(ctx, p), ad.AttemptID); err != nil {
				return err
			}
		}
		if observation.Terminated {
			if err := store.ReleaseExecutionAdmission(ctx, ad, observation, c.now().UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

// RefreshLeases 仅凭当前可信 Controller 与实查工作区续租，终止后释放槽位并留下预算待对账。
// UNKNOWN、到期及 generation 变化都不能获得隐式新租约。
func (c *Coordinator) RefreshLeases(ctx context.Context, fence domain.SchedulerFence) error {
	store, ok := c.store.(recoveryStore)
	if !ok {
		return domain.ErrAdmissionRequired
	}
	admissions, err := store.ListSchedulerAdmissions(ctx, true)
	if err != nil {
		return err
	}
	var failures []error
	for _, ad := range admissions {
		if ad.Fence != fence {
			continue
		}
		observation, found, err := c.admissionObservation(ctx, ad)
		if err != nil || !found {
			continue
		}
		now := c.now().UTC()
		if observation.Terminated {
			if err := store.ReleaseExecutionAdmission(ctx, ad, observation, now); err != nil && !expectedObservationConflict(err) {
				failures = append(failures, err)
			}
			continue
		}
		if ad.AttemptID == "" || observation.Readiness != ports.SessionReadinessReady {
			continue
		}
		p, err := store.LoadExecutionPermit(ctx, ad.AttemptID, fence, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !p.LeaseUntil.After(now) || (p.AttemptState != domain.WorkflowAttemptStateClaimed && p.AttemptState != domain.WorkflowAttemptStateRunning) {
			continue
		}
		if ad.State != domain.AdmissionActive {
			if err := store.RecoverExecutionAdmissionSession(ctx, ad, observation, now); err != nil {
				if !expectedObservationConflict(err) {
					failures = append(failures, err)
				}
				continue
			}
			ad, found, err = store.GetSchedulerAdmission(ctx, ad.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if !found || ad.State != domain.AdmissionActive {
				continue
			}
			p, err = store.LoadExecutionPermit(ctx, ad.AttemptID, fence, now)
			if err != nil {
				failures = append(failures, err)
				continue
			}
		}
		// 异步启动的首次观测可能仍是 provisioning；每轮先对齐 Binding，
		// 不能只在 Leader 接管时恢复，也不能把 ready 推断为业务 Start/Settle。
		if _, err := c.Reconcile(ports.WithExecutionPermit(ctx, p), ad.AttemptID); err != nil {
			if !expectedObservationConflict(err) {
				failures = append(failures, err)
			}
			continue
		}
		p, err = store.LoadExecutionPermit(ctx, ad.AttemptID, fence, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !p.LeaseUntil.After(now) || (p.AttemptState != domain.WorkflowAttemptStateClaimed && p.AttemptState != domain.WorkflowAttemptStateRunning) {
			continue
		}
		states, gate, err := c.repoGate(ctx, ad.AttemptID, observation)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if blockedRepoCode(states, gate) != "" {
			continue
		}
		if p.LeaseUntil.After(now.Add(2 * time.Minute)) {
			continue
		}
		if err := store.RenewExecutionLease(ctx, p, observation, now.Add(2*time.Minute), now); err != nil {
			if !expectedObservationConflict(err) {
				failures = append(failures, fmt.Errorf("renew admission %s: %w", ad.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

// expectedObservationConflict 只跳过单条迟到或不确定观测，不把基础存储与 Leader 错误吞掉。
func expectedObservationConflict(err error) bool {
	return errors.Is(err, domain.ErrSchedulerAdmissionConflict) || errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) || errors.Is(err, domain.ErrWorkflowBindingRevisionConflict)
}

// admissionObservation 优先读取已接受 Session；丢回执只能按稳定 SpawnKey 查找，绝不再次启动。
func (c *Coordinator) admissionObservation(ctx context.Context, ad domain.SchedulerAdmission) (ports.SessionObservation, bool, error) {
	if ad.SessionID != "" {
		ob, err := c.gateway.InspectSession(ctx, ad.SessionID)
		return ob, err == nil, err
	}
	key := ad.ClientRequestID
	if ad.AttemptID != "" {
		binding, found, err := c.store.GetWorkflowAttemptBinding(ctx, ad.AttemptID)
		if err != nil || !found {
			return ports.SessionObservation{}, false, err
		}
		key = binding.SpawnRequestID
	}
	if key == "" {
		return ports.SessionObservation{}, false, nil
	}
	return c.gateway.FindSpawn(ctx, key)
}
