package domain

import "math"

// NewBudgetSnapshot 从明确的空账本开始；后续任一未知事实都会使总额保持未知。
func NewBudgetSnapshot(p BudgetPolicy, window string) BudgetSnapshot {
	return BudgetSnapshot{Policy: p, Window: window, Quality: BudgetQualityExact,
		Tokens: UsageTokenMetrics{InputTokens: new(int64), CachedInputTokens: new(int64), UncachedInputTokens: new(int64), OutputTokens: new(int64)}}
}

// AddUsage 仅累计当前窗口的一次 canonical 投影，不把修正记录重复算入费用。
func (s *BudgetSnapshot) AddUsage(e BudgetUsageEntry) error {
	if e.OccurredAt != nil && s.Policy.Window(*e.OccurredAt) != s.Window {
		return nil
	}
	s.EventCount++
	if e.OccurredAt == nil || e.CostNanos == nil || e.Currency != s.Policy.Currency || (e.Quality != BudgetQualityExact && e.Quality != BudgetQualityEstimated) {
		s.Quality = BudgetQualityUnknown
	} else {
		if err := AddBudgetNanos(&s.KnownCostNanos, *e.CostNanos); err != nil {
			return err
		}
		if e.Quality == BudgetQualityEstimated && s.Quality == BudgetQualityExact {
			s.Quality = BudgetQualityEstimated
		}
	}
	for _, pair := range []struct {
		dst **int64
		src *int64
	}{
		{&s.Tokens.InputTokens, e.Tokens.InputTokens}, {&s.Tokens.CachedInputTokens, e.Tokens.CachedInputTokens},
		{&s.Tokens.UncachedInputTokens, e.Tokens.UncachedInputTokens}, {&s.Tokens.OutputTokens, e.Tokens.OutputTokens},
	} {
		if pair.src == nil || e.OccurredAt == nil {
			*pair.dst = nil
		} else if *pair.dst != nil {
			if err := AddBudgetNanos(*pair.dst, *pair.src); err != nil {
				return err
			}
		}
	}
	return nil
}

// Complete 保留缺失预留和未对账状态；负余额是合法超额，溢出则拒绝生成快照。
func (s *BudgetSnapshot) Complete(reserved *int64, pending bool) error {
	s.ReservedNanos, s.ReconcilePending = reserved, pending
	if pending {
		// 待修订可能降低旧金额，旧投影不再是当前账本的可靠下界。
		s.Quality, s.KnownCostNanos, s.AlertThreshold = BudgetQualityUnknown, 0, 0
		s.Tokens = UsageTokenMetrics{}
		return nil
	}
	if s.Policy.Mode == BudgetSoft {
		for _, threshold := range []int64{80, 95} {
			// 分开商余数，避免 limit*threshold 在大额预算中溢出。
			amount := s.Policy.LimitNanos/100*threshold + (s.Policy.LimitNanos%100*threshold+99)/100
			if s.KnownCostNanos >= amount {
				s.AlertThreshold = threshold
			}
		}
	}
	if s.Quality == BudgetQualityUnknown {
		return nil
	}
	s.CostNanos = new(int64)
	*s.CostNanos = s.KnownCostNanos
	if reserved == nil {
		return nil
	}
	total := s.KnownCostNanos
	if err := AddBudgetNanos(&total, *reserved); err != nil {
		return err
	}
	remaining := s.Policy.LimitNanos - total
	s.RemainingNanos = &remaining
	return nil
}

// AddBudgetNanos 拒绝负费用与整数溢出；负向修正通过替换原费用体现。
func AddBudgetNanos(total *int64, n int64) error {
	if n < 0 || *total < 0 || *total > math.MaxInt64-n {
		return ErrBudgetOverflow
	}
	*total += n
	return nil
}
