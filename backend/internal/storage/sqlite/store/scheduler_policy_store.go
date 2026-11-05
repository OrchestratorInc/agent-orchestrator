package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListSchedulerPolicies 返回已配置策略；未配置的 Adapter 由默认策略闭锁处理。
func (s *Store) ListSchedulerPolicies(ctx context.Context) ([]domain.SchedulerPolicy, error) {
	return schedulerPolicies(ctx, s.qr)
}

// schedulerPolicies 验证持久策略的完整阈值，损坏数据不能扩大准入。
func schedulerPolicies(ctx context.Context, q *gen.Queries) ([]domain.SchedulerPolicy, error) {
	rows, err := q.ListSchedulerPolicies(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.SchedulerPolicy, 0, len(rows))
	for _, r := range rows {
		var p domain.SchedulerPolicy
		if err := json.Unmarshal([]byte(r.PolicyJson), &p); err != nil {
			return nil, err
		}
		p.Revision = r.Revision
		if err := p.Validate(); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}

// PutSchedulerPolicy 仅执行策略 CAS；Operator 身份由控制面端口验证。
func (s *Store) PutSchedulerPolicy(ctx context.Context, p domain.SchedulerPolicy, expected int64, change domain.PolicyChange) (domain.SchedulerPolicy, bool, error) {
	if err := p.Validate(); err != nil {
		return domain.SchedulerPolicy{}, false, err
	}
	if expected < 0 || expected == math.MaxInt64 {
		return domain.SchedulerPolicy{}, false, domain.ErrSchedulerPolicy
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var changed bool
	err := s.inSchedulerTx(ctx, "change scheduler policy", func(q *gen.Queries) error {
		replay, err := policyChangeReplay(ctx, q, "resource", p.ID, change, &p)
		if err != nil || replay {
			return err
		}
		p.Revision = expected + 1
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		n, err := q.PutSchedulerPolicy(ctx, gen.PutSchedulerPolicyParams{ID: p.ID, Scope: p.Scope, ScopeID: p.ScopeID, MaxConcurrent: p.MaxConcurrent, PolicyJson: string(data), NextRevision: p.Revision, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrSchedulerPolicy
		}
		if err := insertPolicyChange(ctx, q, "resource", p.ID, string(data), change); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return p, changed && err == nil, err
}

// ListBudgetPolicies 读取全部层级，调用者不能通过省略某层绕过准入。
func (s *Store) ListBudgetPolicies(ctx context.Context) ([]domain.BudgetPolicy, error) {
	return budgetPolicies(ctx, s.qr)
}

// budgetPolicies 保留可空金额并校验策略作用域，UNKNOWN 不转换成零。
func budgetPolicies(ctx context.Context, q *gen.Queries) ([]domain.BudgetPolicy, error) {
	rows, err := q.ListBudgetPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BudgetPolicy, 0, len(rows))
	for _, r := range rows {
		p := domain.BudgetPolicy{ID: r.ID, Scope: r.Scope, ScopeID: r.ScopeID, Currency: r.Currency, Mode: r.Mode, Period: r.Period, LimitNanos: r.LimitNanos, ReservationNanos: schedulerNanos(r.ReservationNanos), ReservationBasis: r.ReservationBasis, ReservationRef: r.ReservationRef, Revision: r.Revision}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// PutBudgetPolicy 以整数金额和 revision CAS 持久化 Operator 批准的预算。
func (s *Store) PutBudgetPolicy(ctx context.Context, p domain.BudgetPolicy, expected int64, change domain.PolicyChange) (domain.BudgetPolicy, bool, error) {
	if err := p.Validate(); err != nil {
		return domain.BudgetPolicy{}, false, err
	}
	if expected < 0 || expected == math.MaxInt64 {
		return domain.BudgetPolicy{}, false, domain.ErrBudgetPolicy
	}
	p.Revision = expected + 1
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var changed bool
	err := s.inSchedulerTx(ctx, "change budget policy", func(q *gen.Queries) error {
		replay, err := policyChangeReplay(ctx, q, "budget", p.ID, change, &p)
		if err != nil || replay {
			return err
		}
		n, err := q.PutBudgetPolicy(ctx, gen.PutBudgetPolicyParams{ID: p.ID, Scope: p.Scope, ScopeID: p.ScopeID, Currency: p.Currency, Mode: p.Mode, Period: p.Period, LimitNanos: p.LimitNanos, ReservationNanos: schedulerNullNanos(p.ReservationNanos), ReservationBasis: p.ReservationBasis, ReservationRef: p.ReservationRef, NextRevision: p.Revision, ExpectedRevision: expected})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrBudgetPolicy
		}
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if err := insertPolicyChange(ctx, q, "budget", p.ID, string(data), change); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return p, changed && err == nil, err
}

// policyChangeReplay 在 CAS 之前识别完整审批请求的幂等重放。
func policyChangeReplay(ctx context.Context, q *gen.Queries, kind, id string, c domain.PolicyChange, out any) (bool, error) {
	if strings.TrimSpace(c.IdempotencyKey) == "" || len(c.RequestFingerprint) != 64 || strings.TrimSpace(c.ActorRef) == "" || !domain.ValidSchedulerReason(c.Reason) || c.Now.IsZero() {
		return false, domain.ErrSchedulerPolicy
	}
	r, err := q.GetSchedulerPolicyChange(ctx, c.IdempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if r.RequestFingerprint != c.RequestFingerprint || r.Kind != kind || r.PolicyID != id || r.ActorRef != c.ActorRef || r.Reason != c.Reason {
		return false, domain.ErrSchedulerPolicy
	}
	return true, json.Unmarshal([]byte(r.ResultJson), out)
}

func insertPolicyChange(ctx context.Context, q *gen.Queries, kind, id, result string, c domain.PolicyChange) error {
	return q.InsertSchedulerPolicyChange(ctx, gen.InsertSchedulerPolicyChangeParams{IdempotencyKey: c.IdempotencyKey, RequestFingerprint: c.RequestFingerprint, Kind: kind, PolicyID: id, ResultJson: result, ActorRef: c.ActorRef, Reason: c.Reason, CreatedAt: c.Now.UTC()})
}

// ListBudgetReservations 包含旧窗口未结预留，避免跨月遗失预算归属。
func (s *Store) ListBudgetReservations(ctx context.Context, id string) ([]domain.BudgetReservation, error) {
	rows, err := s.qr.ListBudgetReservations(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BudgetReservation, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.BudgetReservation{AdmissionID: r.AdmissionID, AttemptID: domain.WorkflowAttemptID(r.AttemptID.String), PolicyID: r.PolicyID, Window: r.Window, AmountNanos: schedulerNanos(r.AmountNanos), Basis: r.Basis, Reference: r.Reference, State: r.State, Revision: r.Revision})
	}
	return out, nil
}

// schedulerNanos 将未界定的数据库金额保留为 nil。
func schedulerNanos(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// schedulerNullNanos 写入时保持未知与明确零金额的区别。
func schedulerNullNanos(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
func schedulerString(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }
