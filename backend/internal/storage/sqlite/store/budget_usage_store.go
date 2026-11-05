package store

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ReconcileBudgetUsage 在有界 writer 事务中重读 canonical 事实；重复扫描不会新计费或发重复修正。
func (s *Store) ReconcileBudgetUsage(ctx context.Context, limit int64, now time.Time) (int, error) {
	if limit < 1 || limit > 1000 || now.IsZero() {
		return 0, domain.ErrBudgetPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	count := 0
	err := s.inSchedulerTx(ctx, "reconcile budget usage", func(q *gen.Queries) error {
		rows, err := q.ListBudgetReconcileCandidates(ctx, limit)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.PreviousRevision.Valid && r.PreviousRevision.Int64 == math.MaxInt64 {
				return domain.ErrBudgetOverflow
			}
			if err := q.UpsertBudgetUsageAttribution(ctx, gen.UpsertBudgetUsageAttributionParams{
				EventID: r.EventID, SessionID: r.SessionID, AttemptID: r.AttemptID, WorkflowID: r.WorkflowID, TaskID: r.TaskID,
				OccurredAt: r.OccurredAt, Currency: r.Currency, CostNanos: r.CostNanos, Quality: r.Quality, PriceVersion: r.PriceVersion,
				BillingProviderID: r.BillingProviderID, BillingProviderSource: r.BillingProviderSource, MeasurementKind: r.MeasurementKind, Now: now.UTC(),
			}); err != nil {
				return err
			}
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListBudgetUsagePage 提供不含 Provider 原文和路径的有界事件账本，Task 范围包含物化后代。
func (s *Store) ListBudgetUsagePage(ctx context.Context, scope, scopeID string, afterID, limit int64) ([]domain.BudgetUsageEntry, error) {
	if !validBudgetScope(scope, scopeID) || afterID < 0 || limit < 1 || limit > 1000 {
		return nil, domain.ErrBudgetPolicy
	}
	return budgetUsagePage(ctx, s.qr, scope, scopeID, afterID, limit)
}

// budgetUsagePage 与快照共用同一查询和可空值转换，避免分页路径把未知值转为零。
func budgetUsagePage(ctx context.Context, q *gen.Queries, scope, scopeID string, afterID, limit int64) ([]domain.BudgetUsageEntry, error) {
	rows, err := q.ListBudgetUsagePage(ctx, gen.ListBudgetUsagePageParams{Scope: scope, ScopeID: scopeID, AfterID: afterID, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]domain.BudgetUsageEntry, 0, len(rows))
	for _, r := range rows {
		var occurred *time.Time
		if r.OccurredAt.Valid {
			occurred = &r.OccurredAt.Time
		}
		out = append(out, domain.BudgetUsageEntry{EventID: r.EventID, BindingID: r.BindingID, Revision: r.Revision,
			SourceEventKey: r.SourceEventKey, SessionID: domain.SessionID(r.SessionID), AttemptID: domain.WorkflowAttemptID(r.AttemptID),
			WorkflowID: domain.WorkflowID(r.WorkflowID), TaskID: domain.WorkflowTaskID(r.TaskID), OccurredAt: occurred,
			Currency: r.Currency, Quality: r.Quality, CostNanos: schedulerNanos(r.CostNanos), PriceVersion: r.PriceVersion,
			BillingProviderID: r.BillingProviderID, BillingProviderSource: r.BillingProviderSource, MeasurementKind: domain.UsageMeasurementKind(r.MeasurementKind),
			Tokens: domain.UsageTokenMetrics{InputTokens: schedulerNanos(r.InputTokens), CachedInputTokens: schedulerNanos(r.CachedInputTokens), UncachedInputTokens: schedulerNanos(r.UncachedInputTokens), OutputTokens: schedulerNanos(r.OutputTokens)}})
	}
	return out, nil
}

// ReadBudgetSnapshot 用同一 SQLite 读快照查看策略、费用和预留，避免对账或准入并发导致混合余额。
func (s *Store) ReadBudgetSnapshot(ctx context.Context, policyID string, now time.Time) (domain.BudgetSnapshot, error) {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.BudgetSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qr.WithTx(tx)
	policies, err := budgetPolicies(ctx, q)
	if err != nil {
		return domain.BudgetSnapshot{}, err
	}
	for _, p := range policies {
		if p.ID != policyID {
			continue
		}
		snapshot, err := readBudgetSnapshot(ctx, q, p, now)
		if err != nil {
			return domain.BudgetSnapshot{}, err
		}
		return snapshot, tx.Commit()
	}
	return domain.BudgetSnapshot{}, domain.ErrBudgetPolicy
}

// readBudgetSnapshot 分页累计整数费用；未知归属、缺预留或旧活跃会话都不能产生可用余额。
func readBudgetSnapshot(ctx context.Context, q *gen.Queries, p domain.BudgetPolicy, now time.Time) (domain.BudgetSnapshot, error) {
	snapshot := domain.NewBudgetSnapshot(p, p.Window(now))
	for afterID := int64(0); ; {
		page, err := budgetUsagePage(ctx, q, p.Scope, p.ScopeID, afterID, 500)
		if err != nil {
			return snapshot, err
		}
		for _, e := range page {
			if err := snapshot.AddUsage(e); err != nil {
				return snapshot, err
			}
		}
		if len(page) < 500 {
			break
		}
		afterID = page[len(page)-1].EventID
	}
	pending, err := q.CountUnattributedBudgetUsage(ctx)
	if err != nil {
		return snapshot, err
	}
	missing, err := q.CountUnreservedBudgetAdmissions(ctx, gen.CountUnreservedBudgetAdmissionsParams{Scope: p.Scope, ScopeID: p.ScopeID, PolicyID: p.ID})
	if err != nil {
		return snapshot, err
	}
	legacy, err := q.CountLegacyBudgetOccupancy(ctx, gen.CountLegacyBudgetOccupancyParams{Scope: p.Scope, ScopeID: domain.WorkflowID(p.ScopeID)})
	if err != nil {
		return snapshot, err
	}
	amounts, err := q.ListBudgetOutstanding(ctx, gen.ListBudgetOutstandingParams{PolicyID: p.ID, Window: snapshot.Window})
	if err != nil {
		return snapshot, err
	}
	reserved := new(int64)
	for _, n := range amounts {
		if !n.Valid {
			reserved = nil
			break
		}
		if err := domain.AddBudgetNanos(reserved, n.Int64); err != nil {
			return snapshot, err
		}
	}
	if missing > 0 || legacy > 0 {
		reserved = nil
	}
	err = snapshot.Complete(reserved, pending > 0)
	return snapshot, err
}

// ListBudgetUsageCorrections 返回原事件的修订审计；修订金额不得再用于账本求和。
func (s *Store) ListBudgetUsageCorrections(ctx context.Context, eventID int64) ([]domain.BudgetUsageCorrection, error) {
	rows, err := s.qr.ListBudgetUsageCorrections(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BudgetUsageCorrection, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.BudgetUsageCorrection{Key: r.CorrectionKey, EventID: r.EventID,
			Revision: r.Revision, PreviousRevision: r.PreviousRevision, OldCostNanos: schedulerNanos(r.OldCostNanos), NewCostNanos: schedulerNanos(r.NewCostNanos),
			Reason: r.ReasonCode, PreviousSnapshot: r.PreviousSnapshotJson, Snapshot: r.NewSnapshotJson, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// RecordBudgetSoftAlert 使用策略当前 revision 去重，旧快照不能替新策略生成提醒。
func (s *Store) RecordBudgetSoftAlert(ctx context.Context, p domain.BudgetPolicy, window string, threshold int64, now time.Time) (bool, error) {
	if p.Mode != domain.BudgetSoft || window != p.Window(now) || (threshold != 80 && threshold != 95) {
		return false, domain.ErrBudgetPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.RecordBudgetSoftAlert(ctx, gen.RecordBudgetSoftAlertParams{PolicyID: p.ID, Window: window, PolicyRevision: p.Revision, Threshold: threshold, Now: now.UTC()})
	return n == 1, err
}

func validBudgetScope(scope, id string) bool {
	return scope == "global" && id == "" || (scope == "workflow" || scope == "task") && id != ""
}

// ReconcileReleasedBudgetReservations 只结算执行层已可信释放的占位；费用发现未完成时预留保持未知。
// canonical 用量、归因与 finalization 在同一 writer 事务内重读，防止回填与释放竞争。
func (s *Store) ReconcileReleasedBudgetReservations(ctx context.Context, limit int64, now time.Time) (int, error) {
	if limit < 1 || limit > 1000 || now.IsZero() {
		return 0, domain.ErrBudgetPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	count := 0
	err := s.inSchedulerTx(ctx, "settle released budget reservations", func(q *gen.Queries) error {
		if err := q.MarkReleasedBudgetReservationsUncertain(ctx, limit); err != nil {
			return err
		}
		ids, err := q.ListSettleableBudgetAdmissions(ctx, limit)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := q.ConsumeReleasedBudgetReservations(ctx, id); err != nil {
				return err
			}
		}
		count = len(ids)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
