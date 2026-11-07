package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduler"
)

// startM3Scheduler 在启动对账后维护领导权；恢复/扫描和耗时启动均不能阻塞心跳。
// ready 只在本实例首次完成恢复后关闭，取消启动不会伪造 ready。
func startM3Scheduler(ctx context.Context, m *m3Services, boot <-chan struct{}, ready chan<- struct{}, events *cdc.Broadcaster, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	unsubscribe := events.Subscribe(func(event cdc.Event) {
		switch event.Type {
		case cdc.EventWorkflowCreated, cdc.EventWorkflowUpdated, cdc.EventWorkflowTaskUpdated,
			cdc.EventWorkflowAttemptUpdated, cdc.EventSessionCreated, cdc.EventSessionUpdated, cdc.EventSchedulerUpdated:
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	})
	go func() {
		defer sentryobs.RecoverBackground()
		defer close(done)
		defer unsubscribe()
		select {
		case <-ctx.Done():
			return
		case <-boot:
		}
		var workers sync.WaitGroup
		defer workers.Wait()
		var readyOnce sync.Once
		maintenance := make(chan struct{}, 1)
		launches := make(chan struct{}, 10)
		ticker := time.NewTicker(scheduler.SampleInterval)
		defer ticker.Stop()
		scanDue := true
		lastScan := time.Time{}
		for {
			beatCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			leader, err := m.Scheduler.MaintainLeadership(beatCtx)
			cancel()
			if err == nil {
				// 采样器只读本机计数；数据库或探测的慢路径在独立维护任务中运行。
				sampleCtx, cancel := context.WithTimeout(ctx, time.Second)
				_ = m.Scheduler.RefreshResources(sampleCtx)
				cancel()
				select {
				case maintenance <- struct{}{}:
					scan := scanDue || time.Since(lastScan) >= scheduler.ScanInterval
					if scan {
						lastScan, scanDue = time.Now(), false
					}
					workers.Go(func() {
						defer sentryobs.RecoverBackground()
						defer func() { <-maintenance }()
						passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
						defer cancel()
						if err := m.maintain(passCtx, leader); err != nil {
							if ctx.Err() == nil {
								log.Warn("scheduler maintenance deferred", "reason", "recovery_or_evidence_unavailable")
							}
							return
						}
						readyOnce.Do(func() { close(ready) })
						if !scan || len(launches) > cap(launches)-scheduler.ClaimLimit {
							return
						}
						result, tickErr := m.Scheduler.Tick(passCtx)
						// Tick 可能在成功领取后遇错；已提交的领取仍须交付一次。
						for _, dispatch := range result.Dispatches {
							launches <- struct{}{}
							workers.Go(func() {
								defer sentryobs.RecoverBackground()
								defer func() { <-launches }()
								launchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
								defer cancel()
								if err := m.dispatch(launchCtx, dispatch); err != nil && ctx.Err() == nil {
									log.Warn("scheduler dispatch retained for reconciliation", "reason", "execution_not_confirmed")
								}
							})
						}
						if tickErr != nil && ctx.Err() == nil {
							log.Warn("scheduler scan deferred", "reason", "admission_unavailable")
						}
					})
				default:
				}
			}
			// CDC 只记录下一次扫描需求，避免调度器自身 CDC 造成无间隔忙循环。
		wait:
			for {
				select {
				case <-ctx.Done():
					return
				case <-wake:
					scanDue = true
				case <-ticker.C:
					break wait
				}
			}
		}
	}()
	return done
}

// maintain 在同一任期恢复占位和费用，可信续租先于过期处理；失败始终保留原占位。
func (m *m3Services) maintain(ctx context.Context, leader domain.SchedulerLeader) error {
	retired, err := m.retirePlanSessions(ctx, leader.Fence)
	if err != nil {
		return err
	}
	if !leader.Recovered {
		if err := m.Binder.RecoverAdmissions(ctx, leader.Fence); err != nil {
			return err
		}
		if _, err := m.Budget.Reconcile(ctx); err != nil {
			return err
		}
		current, err := m.store.GetSchedulerLeader(ctx)
		if err != nil || current.Fence != leader.Fence {
			return errors.Join(domain.ErrSchedulerFence, err)
		}
		if _, applied, err := m.store.CompleteSchedulerRecovery(ctx, leader.Fence, current.Revision, time.Now().UTC()); err != nil || !applied {
			return errors.Join(domain.ErrSchedulerFence, err)
		}
	}
	if _, err := m.Scheduler.CurrentFence(ctx); err != nil {
		return err
	}
	if err := m.Binder.RefreshLeases(ctx, leader.Fence); err != nil {
		return err
	}
	if err := m.freezePlanArtifacts(ctx, leader.Fence, retired); err != nil {
		return err
	}
	if err := m.startReadyAttempts(ctx, leader.Fence); err != nil {
		return err
	}
	if _, err := m.Workflows.RecoverExpired(ctx); err != nil {
		return err
	}
	_, err = m.Budget.Reconcile(ctx)
	return err
}

// startAfterScheduler 把立即执行首轮的旧观察器延后到恢复门禁，取消时也能正常收尾。
func startAfterScheduler(ctx context.Context, ready <-chan struct{}, start func() <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer sentryobs.RecoverBackground()
		defer close(done)
		select {
		case <-ctx.Done():
			return
		case <-ready:
		}
		if stopped := start(); stopped != nil {
			<-stopped
		}
	}()
	return done
}
