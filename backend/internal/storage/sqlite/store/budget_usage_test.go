package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/budget"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// 删除 canonical 去重、pending 比对或修正触发器，会重复计费或遗漏回填审计。
func TestM3BudgetLedgerCanonicalReplayAndPriceBackfill(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	p := putLedgerPolicy(t, s, "ledger", "global", "", domain.BudgetSoft, 20, now)
	svc := budget.New(s, func() time.Time { return now })
	var firstSource domain.UsageSourceRecord
	for i := 0; i < 2; i++ {
		session := seedUsageSession(t, s, domain.HarnessCodex)
		session.IsTerminated = true
		mustNoError(t, s.UpdateSession(ctx, session))
		source := seedUsageSource(t, s, session, now)
		event := pricedCandidateEvent("same-provider-key")
		event.CreatedAt = now
		mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{event}))
		if i == 0 {
			firstSource = source
			replacement := source
			replacement.ID, replacement.Generation, replacement.ByteOffset = 0, source.Generation+1, 0
			replacement.UpdatedAt = now.Add(time.Second)
			replacement, err := s.ReplaceUsageSource(ctx, source.ID, domain.UsageErrorArtifactReplaced, replacement, now.Add(time.Second))
			mustNoError(t, err)
			mustNoError(t, s.ApplyUsageChunk(ctx, replacement.ID, 0, replacement.UpdatedAt, domain.SourceCursorState{ByteOffset: 20, State: domain.UsageSourceActive, UpdatedAt: now.Add(2 * time.Second)}, []domain.ModelUsageEvent{event}))
		}
	}
	n, err := svc.Reconcile(ctx)
	mustNoError(t, err)
	if n != 2 {
		t.Fatalf("跨 binding 应有两笔、跨 generation 应去重: %d", n)
	}
	unknown, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if unknown.CostNanos != nil || unknown.RemainingNanos != nil || unknown.Quality != domain.BudgetQualityUnknown || unknown.EventCount != 2 {
		t.Fatalf("未知费用被视为零: %+v", unknown)
	}
	candidates, err := s.ListUsageCostCandidates(ctx, "openai", "catalog-v2", 0)
	mustNoError(t, err)
	if len(candidates) != 2 {
		t.Fatalf("cost candidates=%d first=%d", len(candidates), firstSource.ID)
	}
	updates := make([]domain.UsageCostUpdate, 0, len(candidates))
	for _, candidate := range candidates {
		updates = append(updates, domain.UsageCostUpdate{Candidate: candidate, Costs: ledgerCosts(10, "catalog-v2")})
	}
	applied, err := s.ApplyUsageCostUpdates(ctx, updates, now.Add(time.Minute))
	mustNoError(t, err)
	if applied != 2 {
		t.Fatalf("backfill applied=%d", applied)
	}
	pending, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if !pending.ReconcilePending || pending.RemainingNanos != nil {
		t.Fatalf("回填待对账却展示旧余额: %+v", pending)
	}
	base, err := s.LatestSeq(ctx)
	mustNoError(t, err)
	n, err = svc.Reconcile(ctx)
	mustNoError(t, err)
	if n != 2 {
		t.Fatalf("reconciled=%d", n)
	}
	snapshot, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if usageTokenValue(snapshot.CostNanos) != 20 || usageTokenValue(snapshot.RemainingNanos) != 0 || snapshot.Quality != domain.BudgetQualityEstimated || usageTokenValue(snapshot.Tokens.InputTokens) != 10 {
		t.Fatalf("费用或 token 重复计入: %+v", snapshot)
	}
	entries, err := s.ListBudgetUsagePage(ctx, "global", "", 0, 100)
	mustNoError(t, err)
	for _, entry := range entries {
		corrections, err := s.ListBudgetUsageCorrections(ctx, entry.EventID)
		mustNoError(t, err)
		if len(corrections) != 1 || corrections[0].OldCostNanos != nil || usageTokenValue(corrections[0].NewCostNanos) != 10 || entry.Revision != 2 {
			t.Fatalf("回填没有一次修订: %+v %+v", entry, corrections)
		}
	}
	changes, err := s.EventsAfter(ctx, base, 100)
	mustNoError(t, err)
	alerts := 0
	for _, change := range changes {
		if strings.Contains(string(change.Payload), "budget_alert") {
			alerts++
		}
	}
	if alerts != 2 {
		t.Fatalf("80/95 提醒=%d", alerts)
	}
	base, err = s.LatestSeq(ctx)
	mustNoError(t, err)
	n, err = svc.Reconcile(ctx)
	mustNoError(t, err)
	latest, err := s.LatestSeq(ctx)
	mustNoError(t, err)
	if n != 0 || latest != base {
		t.Fatalf("重复对账产生修订或提醒: %d %d->%d", n, base, latest)
	}
}

// 删除对未知时间的处理，会把已定价但时间缺失的事件排除在月预算外。
func TestM3BudgetUnknownTimeClosesMonthlyBalance(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	p := domain.BudgetPolicy{ID: "monthly", Scope: "global", Currency: "USD", Period: domain.BudgetMonthlyUTC, Mode: domain.BudgetHardAdmission, LimitNanos: 100}
	_, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: p.ID, RequestFingerprint: strings.Repeat("c", 64), ActorRef: "test", Reason: "BUDGET_POLICY", Now: now})
	mustNoError(t, err)
	session := seedUsageSession(t, s, domain.HarnessCodex)
	session.IsTerminated = true
	mustNoError(t, s.UpdateSession(ctx, session))
	source := seedUsageSource(t, s, session, now)
	event := pricedCandidateEvent("unknown-time")
	event.Costs = ledgerCosts(30, "catalog-v2")
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{event}))
	_, err = s.ReconcileBudgetUsage(ctx, 100, now)
	mustNoError(t, err)
	snapshot, err := s.ReadBudgetSnapshot(ctx, p.ID, now)
	mustNoError(t, err)
	if snapshot.CostNanos != nil || snapshot.RemainingNanos != nil || snapshot.Quality != domain.BudgetQualityUnknown {
		t.Fatalf("未知时间变成免费月窗口: %+v", snapshot)
	}
}

func ledgerCosts(n int64, version string) domain.UsageEventCosts {
	zero := int64(0)
	return domain.UsageEventCosts{InputCostNanos: &n, CachedInputCostNanos: &zero, OutputCostNanos: &zero, EstimatedCostNanos: &n, PricingVersion: version}
}

func putLedgerPolicy(t *testing.T, s *sqlite.Store, id, scope, scopeID, mode string, limit int64, now time.Time) domain.BudgetPolicy {
	t.Helper()
	p, _, err := s.PutBudgetPolicy(context.Background(), domain.BudgetPolicy{ID: id, Scope: scope, ScopeID: scopeID, Currency: "USD", Mode: mode, Period: domain.BudgetLifetime, LimitNanos: limit}, 0,
		domain.PolicyChange{IdempotencyKey: id, RequestFingerprint: strings.Repeat("a", 64), ActorRef: "ledger-test", Reason: "BUDGET_POLICY", Now: now})
	mustNoError(t, err)
	return p
}
