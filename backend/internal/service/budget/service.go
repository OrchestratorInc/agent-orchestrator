// Package budget 将已有 Usage/Pricing 事实对账为预算投影，不另行采集或估价。
package budget

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store 保留原子读取和有界对账边界，HTTP 不能直接写入费用事实。
type Store interface {
	ReconcileBudgetUsage(context.Context, int64, time.Time) (int, error)
	ListBudgetPolicies(context.Context) ([]domain.BudgetPolicy, error)
	ReadBudgetSnapshot(context.Context, string, time.Time) (domain.BudgetSnapshot, error)
	RecordBudgetSoftAlert(context.Context, domain.BudgetPolicy, string, int64, time.Time) (bool, error)
	ReconcileReleasedBudgetReservations(context.Context, int64, time.Time) (int, error)
	PutBudgetPolicy(context.Context, domain.BudgetPolicy, int64, domain.PolicyChange) (domain.BudgetPolicy, bool, error)
	ListBudgetUsagePage(context.Context, string, string, int64, int64) ([]domain.BudgetUsageEntry, error)
}

// Service 由 Daemon 周期驱动；一次失败保留未完成队列供后续重试。
type Service struct {
	store Store
	now   func() time.Time
}

// New 使用持久化账本创建预算投影服务。
func New(store Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

// Reconcile 每次最多处理 100 条费用变化，再生成按策略/窗口/revision 去重的软提醒。
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	now := s.now().UTC()
	n, err := s.store.ReconcileBudgetUsage(ctx, 100, now)
	if err != nil {
		return 0, err
	}
	if _, err := s.store.ReconcileReleasedBudgetReservations(ctx, 100, now); err != nil {
		return n, err
	}
	policies, err := s.store.ListBudgetPolicies(ctx)
	if err != nil {
		return n, err
	}
	for _, p := range policies {
		if p.Mode != domain.BudgetSoft {
			continue
		}
		snapshot, err := s.store.ReadBudgetSnapshot(ctx, p.ID, now)
		if err != nil {
			return n, err
		}
		if snapshot.ReconcilePending {
			continue
		}
		for _, threshold := range []int64{80, 95} {
			if snapshot.AlertThreshold < threshold {
				continue
			}
			if _, err := s.store.RecordBudgetSoftAlert(ctx, snapshot.Policy, snapshot.Window, threshold, now); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}
