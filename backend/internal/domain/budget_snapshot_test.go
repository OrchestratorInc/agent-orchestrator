package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

// 删除整数溢出检查或 UNKNOWN 传播会生成虚假的正余额。
func TestBudgetSnapshotOverflowAndUnknown(t *testing.T) {
	now := time.Now().UTC()
	p := BudgetPolicy{Currency: "USD", Period: BudgetLifetime, LimitNanos: math.MaxInt64}
	s := NewBudgetSnapshot(p, p.Window(now))
	n := int64(math.MaxInt64)
	e := BudgetUsageEntry{OccurredAt: &now, Currency: "USD", Quality: BudgetQualityEstimated, CostNanos: &n}
	if err := s.AddUsage(e); err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	if err := s.Complete(&one, false); !errors.Is(err, ErrBudgetOverflow) {
		t.Fatalf("费用加预留溢出未拒绝: %v", err)
	}
	e.CostNanos = &one
	if err := s.AddUsage(e); !errors.Is(err, ErrBudgetOverflow) {
		t.Fatalf("费用加费用溢出未拒绝: %v", err)
	}
	s = NewBudgetSnapshot(p, p.Window(now))
	e.CostNanos, e.Quality = nil, BudgetQualityUnknown
	if err := s.AddUsage(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(&one, false); err != nil {
		t.Fatal(err)
	}
	if s.CostNanos != nil || s.RemainingNanos != nil || s.Tokens.InputTokens != nil {
		t.Fatalf("未知值被转成零: %+v", s)
	}
}
