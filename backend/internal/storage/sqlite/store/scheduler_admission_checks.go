package store

import (
	"context"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// checkAdmission 仅信任内部证据并重新读取当前策略和全部占位，动态上限不能扩大政策。
func checkAdmission(ctx context.Context, q *gen.Queries, a domain.SchedulerAdmission, e domain.AdmissionEvidence) ([]gen.InsertBudgetReservationParams, error) {
	due, err := q.ListSchedulerRetryDue(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range due {
		if r.ReasonCode == "RATE_LIMITED" && (r.Harness == string(a.Harness) || r.Harness == "") && r.NextEligibleAt.After(e.Now) {
			return nil, domain.ErrSchedulerCapacity
		}
	}
	c := e.Capability
	// Profile identity is verified against the task's own profile by the caller that
	// knows it (workflow claim); this re-derivation covers the mode and evidence.
	if err := domain.AuthorizeLaunch(c, a.Harness, a.Mode, e.Now); err != nil {
		return nil, err
	}
	global := domain.DefaultSchedulerPolicy("")
	adapter := domain.DefaultSchedulerPolicy(a.Harness)
	policies, err := schedulerPolicies(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, p := range policies {
		if p.ID == global.ID {
			global = p
		}
		if p.ID == adapter.ID {
			adapter = p
		}
	}
	for _, p := range []domain.SchedulerPolicy{global, adapter} {
		if r, ok := e.ExpectedPolicyRevisions[p.ID]; !ok || r != p.Revision {
			return nil, domain.ErrSchedulerPolicy
		}
	}
	if !global.ResourceLimitsDisabled && !validResourceSample(e.Resources, e.Now, global) || e.EffectiveGlobalCap < 1 || e.EffectiveGlobalCap > global.MaxConcurrent || e.EffectiveGlobalCap > domain.SchedulerHardCap {
		return nil, domain.ErrSchedulerCapacity
	}
	occupied, err := q.ListSchedulerOccupancy(ctx, a.SessionID)
	if err != nil {
		return nil, err
	}
	var harnessCount int64
	for _, h := range occupied {
		if h == string(a.Harness) || h == "" {
			harnessCount++
		}
	}
	if int64(len(occupied)) >= e.EffectiveGlobalCap || harnessCount >= adapter.MaxConcurrent {
		return nil, domain.ErrSchedulerCapacity
	}
	return checkAdmissionBudgets(ctx, q, a, e)
}

// validResourceSample 重新计算 CPU 差分并执行 RAM 硬线；任何不可信样本均闭锁。
func validResourceSample(r domain.ResourceSample, now time.Time, p domain.SchedulerPolicy) bool {
	if r.ReasonCode != "" || r.ObservedAt.IsZero() || r.PreviousAt.IsZero() || r.ObservedAt.After(now) || now.Sub(r.ObservedAt) > 15*time.Second || !r.ObservedAt.After(r.PreviousAt) || r.ObservedAt.Sub(r.PreviousAt) > 15*time.Second || r.TotalBytes == 0 || r.AvailableBytes > r.TotalBytes || r.CPUTotalTicks <= r.PreviousCPUTotalTicks || r.CPUBusyTicks < r.PreviousCPUBusyTicks {
		return false
	}
	if r.CPUBusyTicks > r.CPUTotalTicks || r.PreviousCPUBusyTicks > r.PreviousCPUTotalTicks {
		return false
	}
	total, busy := r.CPUTotalTicks-r.PreviousCPUTotalTicks, r.CPUBusyTicks-r.PreviousCPUBusyTicks
	if busy > total {
		return false
	}
	return float64(busy)/float64(total)*100 < float64(p.CPUHardPercent) && float64(r.AvailableBytes)/float64(r.TotalBytes)*100 >= float64(p.RAMHardPercent) && r.AvailableBytes >= uint64(p.RAMFloorBytes) // #nosec G115 -- checkAdmission 仅传默认或 schedulerPolicies 已 Validate 的策略，RAMFloorBytes 非负。
}

// checkAdmissionBudgets 从数据库派生全部适用层及窗口，UNKNOWN 与整数溢出不得作为零。
func checkAdmissionBudgets(ctx context.Context, q *gen.Queries, a domain.SchedulerAdmission, e domain.AdmissionEvidence) ([]gen.InsertBudgetReservationParams, error) {
	ancestors, err := q.ListTaskBudgetAncestors(ctx, string(a.TaskID))
	if err != nil {
		return nil, err
	}
	policies, err := budgetPolicies(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]gen.InsertBudgetReservationParams, 0)
	for _, p := range policies {
		if p.Scope != "global" && (p.Scope != "workflow" || p.ScopeID != string(a.WorkflowID)) && (p.Scope != "task" || !slices.Contains(ancestors, p.ScopeID)) {
			continue
		}
		if revision, ok := e.ExpectedPolicyRevisions[p.RevisionKey()]; !ok || revision != p.Revision {
			return nil, domain.ErrBudgetPolicy
		}
		amount, basis, ref := p.ReservationNanos, p.ReservationBasis, p.ReservationRef
		if amount == nil {
			amount, basis, ref = e.ReservationNanos, e.ReservationBasis, e.ReservationRef
		}
		if amount != nil && (*amount < 0 || !domain.ValidReservationBasis(basis, ref)) {
			return nil, domain.ErrBudgetUnknown
		}
		window := p.Window(e.Now)
		if p.Mode == domain.BudgetHardAdmission {
			if amount == nil {
				return nil, domain.ErrBudgetUnknown
			}
			unreserved, err := q.CountUnreservedBudgetAdmissions(ctx, gen.CountUnreservedBudgetAdmissionsParams{Scope: p.Scope, ScopeID: p.ScopeID, PolicyID: p.ID})
			if err != nil {
				return nil, err
			}
			if unreserved > 0 {
				return nil, domain.ErrBudgetUnknown
			}
			legacy, err := q.CountLegacyBudgetOccupancy(ctx, gen.CountLegacyBudgetOccupancyParams{Scope: p.Scope, ScopeID: domain.WorkflowID(p.ScopeID)})
			if err != nil {
				return nil, err
			}
			if legacy > 0 {
				return nil, domain.ErrBudgetUnknown
			}
			missing, err := q.CountUnattributedBudgetUsage(ctx)
			if err != nil {
				return nil, err
			}
			if missing > 0 {
				return nil, domain.ErrBudgetUnknown
			}
			total := int64(0)
			outstanding, err := q.ListBudgetOutstanding(ctx, gen.ListBudgetOutstandingParams{PolicyID: p.ID, Window: window})
			if err != nil {
				return nil, err
			}
			for _, n := range outstanding {
				if !n.Valid {
					return nil, domain.ErrBudgetUnknown
				}
				if err := domain.AddBudgetNanos(&total, n.Int64); err != nil {
					return nil, err
				}
			}
			usage, err := q.ListBudgetUsage(ctx, gen.ListBudgetUsageParams{Scope: p.Scope, ScopeID: p.ScopeID})
			if err != nil {
				return nil, err
			}
			for _, u := range usage {
				if !u.OccurredAt.Valid {
					return nil, domain.ErrBudgetUnknown
				}
				if p.Window(u.OccurredAt.Time) != window {
					continue
				}
				if !u.CostNanos.Valid || u.Quality == "UNKNOWN" || u.Currency != p.Currency {
					return nil, domain.ErrBudgetUnknown
				}
				if err := domain.AddBudgetNanos(&total, u.CostNanos.Int64); err != nil {
					return nil, err
				}
			}
			if err := domain.AddBudgetNanos(&total, *amount); err != nil {
				return nil, err
			}
			if total > p.LimitNanos {
				return nil, domain.ErrBudgetExceeded
			}
		}
		out = append(out, gen.InsertBudgetReservationParams{AdmissionID: a.ID, AttemptID: schedulerString(string(a.AttemptID)), PolicyID: p.ID, Window: window, AmountNanos: schedulerNullNanos(amount), Basis: basis, Reference: ref})
	}
	return out, nil
}
