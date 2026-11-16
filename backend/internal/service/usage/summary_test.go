package usage

import (
	"context"
	"math"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type usageSummaryStoreStub struct {
	projectID domain.ProjectID
	rows      []domain.CompactSessionUsageAggregate
	calls     int
}

func (s *usageSummaryStoreStub) ListCompactSessionUsageAggregates(_ context.Context, id domain.ProjectID) ([]domain.CompactSessionUsageAggregate, error) {
	s.projectID, s.calls = id, s.calls+1
	return s.rows, nil
}

func TestSummaryReaderListCompactUsesOneBatchRead(t *testing.T) {
	zeroProcessed, partialProcessed := int64(0), int64(120)
	store := &usageSummaryStoreStub{rows: []domain.CompactSessionUsageAggregate{
		{
			SessionID: "zero", ProcessedTokens: &zeroProcessed,
			Cost: completeCostAggregate(1, 0, 0, 0, 0),
		},
		{
			SessionID: "partial", ProcessedTokens: &partialProcessed, Incomplete: true,
			Cost: domain.UsageCostAggregate{
				EventCount:                    1,
				ObservedCostEventCount:        1,
				KnownInputCount:               1,
				KnownInputNanos:               30,
				UnpricedKnownInputNanos:       30,
				KnownCachedInputCount:         1,
				KnownCachedInputNanos:         0,
				UnpricedKnownCachedInputNanos: 0,
				KnownOutputCount:              1,
				KnownOutputNanos:              5,
				UnpricedKnownOutputNanos:      5,
			},
		},
	}}

	got, err := NewSummaryReader(store).ListCompact(context.Background(), "reverb")
	mustNoError(t, err)
	if store.calls != 1 || store.projectID != "reverb" || len(got) != 2 {
		t.Fatalf("read=%d project=%q items=%+v", store.calls, store.projectID, got)
	}
	if got[0].EstimatedCost == nil || got[0].EstimatedCost.Coverage != domain.EstimatedCostCoverageComplete || got[0].EstimatedCost.TotalNanos != 0 {
		t.Fatalf("zero cost = %+v, want complete zero", got[0].EstimatedCost)
	}
	if got[1].ProcessedTokens == nil || *got[1].ProcessedTokens != 120 || !got[1].Incomplete ||
		got[1].EstimatedCost == nil ||
		got[1].EstimatedCost.Coverage != domain.EstimatedCostCoveragePartial || got[1].EstimatedCost.TotalNanos != 35 {
		t.Fatalf("partial compact summary = %+v", got[1])
	}
	if got[1].EstimatedCost.InputNanos == nil || *got[1].EstimatedCost.InputNanos != 30 ||
		got[1].EstimatedCost.CachedInputNanos == nil || *got[1].EstimatedCost.CachedInputNanos != 0 {
		t.Fatalf("partial components = %+v", got[1].EstimatedCost)
	}
}

// Break caught: inferred prices were aggregated into the same dollar value as
// observed prices without preserving the distinction the UI needs to explain
// that the billing provider has not been confirmed.
func TestSummaryReaderReportsCostProviderAttribution(t *testing.T) {
	observed := completeCostAggregate(1, 100, 20, 10, 70)
	inferred := completeCostAggregate(1, 200, 40, 20, 140)
	inferred.ObservedCostEventCount, inferred.InferredCostEventCount = 0, 1
	mixed := completeCostAggregate(2, 300, 60, 30, 210)
	mixed.ObservedCostEventCount, mixed.InferredCostEventCount = 1, 1
	store := &usageSummaryStoreStub{rows: []domain.CompactSessionUsageAggregate{
		{SessionID: "observed", Cost: observed},
		{SessionID: "inferred", Cost: inferred},
		{SessionID: "mixed", Cost: mixed},
	}}

	got, err := NewSummaryReader(store).ListCompact(context.Background(), "")
	mustNoError(t, err)
	want := []domain.EstimatedCostProviderAttribution{
		domain.EstimatedCostProviderAttributionObserved,
		domain.EstimatedCostProviderAttributionInferred,
		domain.EstimatedCostProviderAttributionMixed,
	}
	if len(got) != len(want) {
		t.Fatalf("items = %+v", got)
	}
	for i, item := range got {
		if item.EstimatedCost == nil || item.EstimatedCost.ProviderAttribution != want[i] {
			t.Fatalf("%s attribution = %+v, want %s", item.SessionID, item.EstimatedCost, want[i])
		}
	}
}

func TestSummaryReaderReturnsUnavailableCostForZeroPartialLowerBound(t *testing.T) {
	store := &usageSummaryStoreStub{rows: []domain.CompactSessionUsageAggregate{{
		SessionID: "unknown", Cost: domain.UsageCostAggregate{EventCount: 1},
	}}}
	got, err := NewSummaryReader(store).ListCompact(context.Background(), "")
	mustNoError(t, err)
	if len(got) != 1 || got[0].EstimatedCost != nil {
		t.Fatalf("cost = %+v, want unavailable", got)
	}
}

func TestSummaryReaderRejectsAggregateOverflow(t *testing.T) {
	t.Run("partial lower bound", func(t *testing.T) {
		store := &usageSummaryStoreStub{rows: []domain.CompactSessionUsageAggregate{{
			SessionID: "overflow",
			Cost: domain.UsageCostAggregate{
				EventCount:              2,
				PricedEventCount:        1,
				PricedTotalNanos:        math.MaxInt64,
				KnownInputCount:         1,
				KnownInputNanos:         1,
				UnpricedKnownInputNanos: 1,
			},
		}}}
		if _, err := NewSummaryReader(store).ListCompact(context.Background(), ""); err == nil {
			t.Fatal("partial cost overflow returned nil error")
		}
	})

}

func testUsageMetrics(input, cachedInput, uncachedInput, output int64) domain.UsageTokenMetrics {
	return domain.UsageTokenMetrics{
		InputTokens: &input, CachedInputTokens: &cachedInput, UncachedInputTokens: &uncachedInput,
		OutputTokens: &output,
	}
}

func completeCostAggregate(events, total, input, cachedInput, output int64) domain.UsageCostAggregate {
	return domain.UsageCostAggregate{
		EventCount: events, PricedEventCount: events, PricedTotalNanos: total,
		ObservedCostEventCount: events,
		KnownInputCount:        events, KnownInputNanos: input,
		KnownCachedInputCount: events, KnownCachedInputNanos: cachedInput,
		KnownOutputCount: events, KnownOutputNanos: output,
	}
}
