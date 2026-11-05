package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除 pending 比对或以 correction 再记一笔，会让价格下修后的余额继续使用旧费用或双计。
func TestM3BudgetNegativeCorrectionReplacesOriginalCost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := workflowStoreTime()
	p := putLedgerPolicy(t, s, "correction", "global", "", domain.BudgetObserve, 100, now)
	session := seedUsageSession(t, s, domain.HarnessCodex)
	session.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, session))
	source := seedUsageSource(t, s, session, now)
	event := pricedCandidateEvent("corrected-provider")
	event.CreatedAt = now
	event.BillingProviderSource = domain.UsageBillingProviderInferred
	event.Costs = ledgerCosts(50, "inferred-v1")
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{event}))
	_, err := s.ReconcileBudgetUsage(ctx, 100, now)
	mustNoError(t, err)
	before, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if usageTokenValue(before.CostNanos) != 50 {
		t.Fatalf("初始费用: %+v", before)
	}
	legacy, err := s.ListLegacyUsageEvents(ctx, source.ID)
	mustNoError(t, err)
	current, found, err := s.GetUsageSourceForIngestion(ctx, source.ID)
	mustNoError(t, err)
	if len(legacy) != 1 || !found {
		t.Fatal("缺少可修订来源")
	}
	repair := domain.LegacyUsageRepair{Candidate: legacy[0], ExpectedFileIdentity: current.Source.FileIdentity,
		ExpectedByteOffset: current.Source.ByteOffset, ExpectedParserStateJSON: current.Source.ParserStateJSON, ExpectedSourceUpdatedAt: current.Source.UpdatedAt,
		BillingProviderID: "confirmed-provider", BillingProviderSource: domain.UsageBillingProviderObserved,
		ProviderUsageJSON: event.ProviderUsageJSON, Costs: ledgerCosts(20, "observed-v2")}
	n, err := s.ApplyLegacyUsageRepairs(ctx, []domain.LegacyUsageRepair{repair}, now)
	mustNoError(t, err)
	if n != 1 {
		t.Fatalf("价格下修次数=%d", n)
	}
	pending, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if !pending.ReconcilePending || pending.KnownCostNanos != 0 || pending.CostNanos != nil {
		t.Fatalf("旧费用被当成当前下界: %+v", pending)
	}
	_, err = s.ReconcileBudgetUsage(ctx, 100, now)
	mustNoError(t, err)
	after, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if usageTokenValue(after.CostNanos) != 20 || usageTokenValue(after.RemainingNanos) != 80 || after.EventCount != 1 {
		t.Fatalf("下修没有替换原事件: %+v", after)
	}
	rows, err := s.ListBudgetUsagePage(ctx, "global", "", 0, 100)
	mustNoError(t, err)
	if len(rows) != 1 {
		t.Fatalf("events=%d", len(rows))
	}
	corrections, err := s.ListBudgetUsageCorrections(ctx, rows[0].EventID)
	mustNoError(t, err)
	if len(corrections) != 1 || usageTokenValue(corrections[0].OldCostNanos) != 50 || usageTokenValue(corrections[0].NewCostNanos) != 20 {
		t.Fatalf("下修审计=%+v", corrections)
	}
	n, err = s.ApplyLegacyUsageRepairs(ctx, []domain.LegacyUsageRepair{repair}, now)
	mustNoError(t, err)
	if n != 0 {
		t.Fatalf("旧来源重放又修订=%d", n)
	}
}
